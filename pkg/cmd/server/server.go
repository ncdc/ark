/*
Copyright 2017 the Heptio Ark contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kcorev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	api "github.com/heptio/ark/pkg/apis/ark/v1"
	"github.com/heptio/ark/pkg/backup"
	"github.com/heptio/ark/pkg/buildinfo"
	"github.com/heptio/ark/pkg/client"
	"github.com/heptio/ark/pkg/cloudprovider"
	"github.com/heptio/ark/pkg/cmd"
	"github.com/heptio/ark/pkg/cmd/util/flag"
	"github.com/heptio/ark/pkg/controller"
	arkdiscovery "github.com/heptio/ark/pkg/discovery"
	clientset "github.com/heptio/ark/pkg/generated/clientset/versioned"
	arkv1client "github.com/heptio/ark/pkg/generated/clientset/versioned/typed/ark/v1"
	informers "github.com/heptio/ark/pkg/generated/informers/externalversions"
	"github.com/heptio/ark/pkg/plugin"
	"github.com/heptio/ark/pkg/restic"
	"github.com/heptio/ark/pkg/restore"
	"github.com/heptio/ark/pkg/util/kube"
	"github.com/heptio/ark/pkg/util/logging"
	"github.com/heptio/ark/pkg/util/stringslice"
)

func NewCommand() *cobra.Command {
	var (
		sortedLogLevels = getSortedLogLevels()
		logLevelFlag    = flag.NewEnum(logrus.InfoLevel.String(), sortedLogLevels...)
		pluginDir       = "/plugins"
	)

	var command = &cobra.Command{
		Use:   "server",
		Short: "Run the ark server",
		Long:  "Run the ark server",
		Run: func(c *cobra.Command, args []string) {
			logLevel := logrus.InfoLevel

			if parsed, err := logrus.ParseLevel(logLevelFlag.String()); err == nil {
				logLevel = parsed
			} else {
				// This should theoretically never happen assuming the enum flag
				// is constructed correctly because the enum flag will not allow
				//  an invalid value to be set.
				logrus.Errorf("log-level flag has invalid value %s", strings.ToUpper(logLevelFlag.String()))
			}
			logrus.Infof("setting log-level to %s", strings.ToUpper(logLevel.String()))

			logger := newLogger(logLevel, &logging.ErrorLocationHook{}, &logging.LogLocationHook{})
			logger.Infof("Starting Ark server %s", buildinfo.FormattedGitSHA())

			// NOTE: the namespace flag is bound to ark's persistent flags when the root ark command
			// creates the client Factory and binds the Factory's flags. We're not using a Factory here in
			// the server because the Factory gets its basename set at creation time, and the basename is
			// used to construct the user-agent for clients. Also, the Factory's Namespace() method uses
			// the client config file to determine the appropriate namespace to use, and that isn't
			// applicable to the server (it uses the method directly below instead). We could potentially
			// add a SetBasename() method to the Factory, and tweak how Namespace() works, if we wanted to
			// have the server use the Factory.
			namespaceFlag := c.Flag("namespace")
			if namespaceFlag == nil {
				cmd.CheckError(errors.New("unable to look up namespace flag"))
			}
			namespace := getServerNamespace(namespaceFlag)

			s, err := newServer(namespace, fmt.Sprintf("%s-%s", c.Parent().Name(), c.Name()), pluginDir, logger)

			cmd.CheckError(err)

			cmd.CheckError(s.run())
		},
	}

	command.Flags().Var(logLevelFlag, "log-level", fmt.Sprintf("the level at which to log. Valid values are %s.", strings.Join(sortedLogLevels, ", ")))
	command.Flags().StringVar(&pluginDir, "plugin-dir", pluginDir, "directory containing Ark plugins")

	return command
}

func getServerNamespace(namespaceFlag *pflag.Flag) string {
	if namespaceFlag.Changed {
		return namespaceFlag.Value.String()
	}

	if data, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		if ns := strings.TrimSpace(string(data)); len(ns) > 0 {
			return ns
		}
	}

	return api.DefaultNamespace
}

func newLogger(level logrus.Level, hooks ...logrus.Hook) *logrus.Logger {
	logger := logrus.New()
	logger.Level = level

	for _, hook := range hooks {
		logger.Hooks.Add(hook)
	}

	return logger
}

// getSortedLogLevels returns a string slice containing all of the valid logrus
// log levels (based on logrus.AllLevels), sorted in ascending order of severity.
func getSortedLogLevels() []string {
	var (
		sortedLogLevels  = make([]logrus.Level, len(logrus.AllLevels))
		logLevelsStrings []string
	)

	copy(sortedLogLevels, logrus.AllLevels)

	// logrus.Panic has the lowest value, so the compare function uses ">"
	sort.Slice(sortedLogLevels, func(i, j int) bool { return sortedLogLevels[i] > sortedLogLevels[j] })

	for _, level := range sortedLogLevels {
		logLevelsStrings = append(logLevelsStrings, level.String())
	}

	return logLevelsStrings
}

type server struct {
	namespace             string
	kubeClientConfig      *rest.Config
	kubeClient            kubernetes.Interface
	arkClient             clientset.Interface
	objectStore           cloudprovider.ObjectStore
	backupService         cloudprovider.BackupService
	snapshotService       cloudprovider.SnapshotService
	discoveryClient       discovery.DiscoveryInterface
	clientPool            dynamic.ClientPool
	sharedInformerFactory informers.SharedInformerFactory
	ctx                   context.Context
	cancelFunc            context.CancelFunc
	logger                logrus.FieldLogger
	pluginManager         plugin.Manager
	resticManager         restic.RepositoryManager
}

func newServer(namespace, baseName, pluginDir string, logger *logrus.Logger) (*server, error) {
	clientConfig, err := client.Config("", "", baseName)
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	arkClient, err := clientset.NewForConfig(clientConfig)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	pluginManager, err := plugin.NewManager(logger, logger.Level, pluginDir)
	if err != nil {
		return nil, err
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	s := &server{
		namespace:             namespace,
		kubeClientConfig:      clientConfig,
		kubeClient:            kubeClient,
		arkClient:             arkClient,
		discoveryClient:       arkClient.Discovery(),
		clientPool:            dynamic.NewDynamicClientPool(clientConfig),
		sharedInformerFactory: informers.NewFilteredSharedInformerFactory(arkClient, 0, namespace, nil),
		ctx:           ctx,
		cancelFunc:    cancelFunc,
		logger:        logger,
		pluginManager: pluginManager,
	}

	return s, nil
}

func (s *server) run() error {
	defer s.pluginManager.CleanupClients()
	s.handleShutdownSignals()

	if err := s.ensureArkNamespace(); err != nil {
		return err
	}

	originalConfig, err := s.loadConfig()
	if err != nil {
		return err
	}

	// watchConfig needs to examine the unmodified original config, so we keep that around as a
	// separate object, and instead apply defaults to a clone.
	config := originalConfig.DeepCopy()
	applyConfigDefaults(config, s.logger)

	s.watchConfig(originalConfig)

	if err := s.initBackupService(config); err != nil {
		return err
	}

	if err := s.initSnapshotService(config); err != nil {
		return err
	}

	if err := s.initResticManager(config); err != nil {
		return err
	}

	if err := s.runControllers(config); err != nil {
		return err
	}

	return nil
}

func (s *server) ensureArkNamespace() error {
	logContext := s.logger.WithField("namespace", s.namespace)

	logContext.Info("Ensuring namespace exists for backups")
	defaultNamespace := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.namespace,
		},
	}

	if created, err := kube.EnsureNamespaceExists(&defaultNamespace, s.kubeClient.CoreV1().Namespaces()); created {
		logContext.Info("Namespace created")
	} else if err != nil {
		return err
	}
	logContext.Info("Namespace already exists")
	return nil
}

func (s *server) loadConfig() (*api.Config, error) {
	s.logger.Info("Retrieving Ark configuration")
	var (
		config *api.Config
		err    error
	)
	for {
		config, err = s.arkClient.ArkV1().Configs(s.namespace).Get("default", metav1.GetOptions{})
		if err == nil {
			break
		}
		if !apierrors.IsNotFound(err) {
			s.logger.WithError(err).Error("Error retrieving configuration")
		} else {
			s.logger.Info("Configuration not found")
		}
		s.logger.Info("Will attempt to retrieve configuration again in 5 seconds")
		time.Sleep(5 * time.Second)
	}
	s.logger.Info("Successfully retrieved Ark configuration")
	return config, nil
}

const (
	defaultGCSyncPeriod       = 60 * time.Minute
	defaultBackupSyncPeriod   = 60 * time.Minute
	defaultScheduleSyncPeriod = time.Minute
)

var defaultResourcePriorities = []string{
	"namespaces",
	"persistentvolumes",
	"persistentvolumeclaims",
	"secrets",
	"configmaps",
	"serviceaccounts",
	"limitranges",
	"pods",
}

func applyConfigDefaults(c *api.Config, logger logrus.FieldLogger) {
	if c.GCSyncPeriod.Duration == 0 {
		c.GCSyncPeriod.Duration = defaultGCSyncPeriod
	}

	if c.BackupSyncPeriod.Duration == 0 {
		c.BackupSyncPeriod.Duration = defaultBackupSyncPeriod
	}

	if c.ScheduleSyncPeriod.Duration == 0 {
		c.ScheduleSyncPeriod.Duration = defaultScheduleSyncPeriod
	}

	if len(c.ResourcePriorities) == 0 {
		c.ResourcePriorities = defaultResourcePriorities
		logger.WithField("priorities", c.ResourcePriorities).Info("Using default resource priorities")
	} else {
		logger.WithField("priorities", c.ResourcePriorities).Info("Using resource priorities from config")
	}

	if c.BackupStorageProvider.Config == nil {
		c.BackupStorageProvider.Config = make(map[string]string)
	}

	// add the bucket name to the config map so that object stores can use
	// it when initializing. The AWS object store uses this to determine the
	// bucket's region when setting up its client.
	c.BackupStorageProvider.Config["bucket"] = c.BackupStorageProvider.Bucket
}

// watchConfig adds an update event handler to the Config shared informer, invoking s.cancelFunc
// when it sees a change.
func (s *server) watchConfig(config *api.Config) {
	s.sharedInformerFactory.Ark().V1().Configs().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			updated := newObj.(*api.Config)
			s.logger.WithField("name", kube.NamespaceAndName(updated)).Debug("received updated config")

			if updated.Name != config.Name {
				s.logger.WithField("name", updated.Name).Debug("Config watch channel received other config")
				return
			}

			// Objects retrieved via Get() don't have their Kind or APIVersion set. Objects retrieved via
			// Watch(), including those from shared informer event handlers, DO have their Kind and
			// APIVersion set. To prevent the DeepEqual() call below from considering Kind or APIVersion
			// as the source of a change, set config.Kind and config.APIVersion to match the values from
			// the updated Config.
			if config.Kind != updated.Kind {
				config.Kind = updated.Kind
			}
			if config.APIVersion != updated.APIVersion {
				config.APIVersion = updated.APIVersion
			}

			if !reflect.DeepEqual(config, updated) {
				s.logger.Info("Detected a config change. Gracefully shutting down")
				s.cancelFunc()
			}
		},
	})
}

func (s *server) handleShutdownSignals() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		s.logger.Infof("Received signal %s, gracefully shutting down", sig)
		s.cancelFunc()
	}()
}

func (s *server) initBackupService(config *api.Config) error {
	s.logger.Info("Configuring cloud provider for backup service")
	objectStore, err := getObjectStore(config.BackupStorageProvider.CloudProviderConfig, s.pluginManager)
	if err != nil {
		return err
	}

	s.objectStore = objectStore
	s.backupService = cloudprovider.NewBackupService(objectStore, s.logger)
	return nil
}

func (s *server) initSnapshotService(config *api.Config) error {
	if config.PersistentVolumeProvider == nil {
		s.logger.Info("PersistentVolumeProvider config not provided, volume snapshots and restores are disabled")
		return nil
	}

	s.logger.Info("Configuring cloud provider for snapshot service")
	blockStore, err := getBlockStore(*config.PersistentVolumeProvider, s.pluginManager)
	if err != nil {
		return err
	}
	s.snapshotService = cloudprovider.NewSnapshotService(blockStore)
	return nil
}

func getObjectStore(cloudConfig api.CloudProviderConfig, manager plugin.Manager) (cloudprovider.ObjectStore, error) {
	if cloudConfig.Name == "" {
		return nil, errors.New("object storage provider name must not be empty")
	}

	objectStore, err := manager.GetObjectStore(cloudConfig.Name)
	if err != nil {
		return nil, err
	}

	if err := objectStore.Init(cloudConfig.Config); err != nil {
		return nil, err
	}

	return objectStore, nil
}

func getBlockStore(cloudConfig api.CloudProviderConfig, manager plugin.Manager) (cloudprovider.BlockStore, error) {
	if cloudConfig.Name == "" {
		return nil, errors.New("block storage provider name must not be empty")
	}

	blockStore, err := manager.GetBlockStore(cloudConfig.Name)
	if err != nil {
		return nil, err
	}

	if err := blockStore.Init(cloudConfig.Config); err != nil {
		return nil, err
	}

	return blockStore, nil
}

func durationMin(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (s *server) initResticManager(config *api.Config) error {
	s.resticManager = restic.NewRepositoryManager(
		s.objectStore,
		restic.BackendType(config.BackupStorageProvider.Name),
		"ark-restic-backups", // TODO need to get the restic bucket name from config somwehere
		s.kubeClient.CoreV1().Secrets(s.namespace),
		s.logger,
	)

	s.logger.Info("Checking restic repositories")
	return s.resticManager.CheckAllRepos()
}

func (s *server) runControllers(config *api.Config) error {
	s.logger.Info("Starting controllers")

	ctx := s.ctx
	var wg sync.WaitGroup

	cloudBackupCacheResyncPeriod := durationMin(config.GCSyncPeriod.Duration, config.BackupSyncPeriod.Duration)
	s.logger.Infof("Caching cloud backups every %s", cloudBackupCacheResyncPeriod)
	s.backupService = cloudprovider.NewBackupServiceWithCachedBackupGetter(
		ctx,
		s.backupService,
		cloudBackupCacheResyncPeriod,
		s.logger,
	)

	backupSyncController := controller.NewBackupSyncController(
		s.arkClient.ArkV1(),
		s.backupService,
		config.BackupStorageProvider.Bucket,
		config.BackupSyncPeriod.Duration,
		s.namespace,
		s.logger,
	)
	wg.Add(1)
	go func() {
		backupSyncController.Run(ctx, 1)
		wg.Done()
	}()

	discoveryHelper, err := arkdiscovery.NewHelper(s.discoveryClient, s.logger)
	if err != nil {
		return err
	}
	go wait.Until(
		func() {
			if err := discoveryHelper.Refresh(); err != nil {
				s.logger.WithError(err).Error("Error refreshing discovery")
			}
		},
		5*time.Minute,
		ctx.Done(),
	)

	if config.RestoreOnlyMode {
		s.logger.Info("Restore only mode - not starting the backup, schedule, delete-backup, or GC controllers")
	} else {
		backupTracker := controller.NewBackupTracker()

		backupper, err := newBackupper(discoveryHelper, s.clientPool, s.backupService, s.snapshotService, s.kubeClientConfig, s.kubeClient.CoreV1(), s.namespace, s.resticManager)
		cmd.CheckError(err)
		backupController := controller.NewBackupController(
			s.sharedInformerFactory.Ark().V1().Backups(),
			s.arkClient.ArkV1(),
			backupper,
			s.backupService,
			config.BackupStorageProvider.Bucket,
			s.snapshotService != nil,
			s.logger,
			s.pluginManager,
			backupTracker,
		)
		wg.Add(1)
		go func() {
			backupController.Run(ctx, 1)
			wg.Done()
		}()

		scheduleController := controller.NewScheduleController(
			s.namespace,
			s.arkClient.ArkV1(),
			s.arkClient.ArkV1(),
			s.sharedInformerFactory.Ark().V1().Schedules(),
			config.ScheduleSyncPeriod.Duration,
			s.logger,
		)
		wg.Add(1)
		go func() {
			scheduleController.Run(ctx, 1)
			wg.Done()
		}()

		gcController := controller.NewGCController(
			s.logger,
			s.sharedInformerFactory.Ark().V1().Backups(),
			s.arkClient.ArkV1(),
			config.GCSyncPeriod.Duration,
		)
		wg.Add(1)
		go func() {
			gcController.Run(ctx, 1)
			wg.Done()
		}()

		backupDeletionController := controller.NewBackupDeletionController(
			s.logger,
			s.sharedInformerFactory.Ark().V1().DeleteBackupRequests(),
			s.arkClient.ArkV1(), // deleteBackupRequestClient
			s.arkClient.ArkV1(), // backupClient
			s.snapshotService,
			s.backupService,
			config.BackupStorageProvider.Bucket,
			s.sharedInformerFactory.Ark().V1().Restores(),
			s.arkClient.ArkV1(), // restoreClient
			backupTracker,
			s.resticManager,
		)
		wg.Add(1)
		go func() {
			backupDeletionController.Run(ctx, 1)
			wg.Done()
		}()

	}

	restorer, err := newRestorer(
		discoveryHelper,
		s.clientPool,
		s.backupService,
		s.snapshotService,
		config.ResourcePriorities,
		s.arkClient.ArkV1(),
		s.kubeClient,
		s.logger,
	)
	cmd.CheckError(err)

	restoreController := controller.NewRestoreController(
		s.namespace,
		s.sharedInformerFactory.Ark().V1().Restores(),
		s.arkClient.ArkV1(),
		s.arkClient.ArkV1(),
		restorer,
		s.backupService,
		config.BackupStorageProvider.Bucket,
		s.sharedInformerFactory.Ark().V1().Backups(),
		s.snapshotService != nil,
		s.logger,
		s.pluginManager,
	)
	wg.Add(1)
	go func() {
		restoreController.Run(ctx, 1)
		wg.Done()
	}()

	downloadRequestController := controller.NewDownloadRequestController(
		s.arkClient.ArkV1(),
		s.sharedInformerFactory.Ark().V1().DownloadRequests(),
		s.sharedInformerFactory.Ark().V1().Restores(),
		s.backupService,
		config.BackupStorageProvider.Bucket,
		s.logger,
	)
	wg.Add(1)
	go func() {
		downloadRequestController.Run(ctx, 1)
		wg.Done()
	}()

	// SHARED INFORMERS HAVE TO BE STARTED AFTER ALL CONTROLLERS
	go s.sharedInformerFactory.Start(ctx.Done())

	// Remove this sometime after v0.8.0
	cache.WaitForCacheSync(ctx.Done(), s.sharedInformerFactory.Ark().V1().Backups().Informer().HasSynced)
	s.removeDeprecatedGCFinalizer()

	s.logger.Info("Server started successfully")

	<-ctx.Done()

	s.logger.Info("Waiting for all controllers to shut down gracefully")
	wg.Wait()

	return nil
}

const gcFinalizer = "gc.ark.heptio.com"

func (s *server) removeDeprecatedGCFinalizer() {
	backups, err := s.sharedInformerFactory.Ark().V1().Backups().Lister().List(labels.Everything())
	if err != nil {
		s.logger.WithError(errors.WithStack(err)).Error("error listing backups from cache - unable to remove old finalizers")
		return
	}

	for _, backup := range backups {
		log := s.logger.WithField("backup", kube.NamespaceAndName(backup))

		if !stringslice.Has(backup.Finalizers, gcFinalizer) {
			log.Debug("backup doesn't have deprecated finalizer - skipping")
			continue
		}

		log.Info("removing deprecated finalizer from backup")

		patch := map[string]interface{}{
			"metadata": map[string]interface{}{
				"finalizers":      stringslice.Except(backup.Finalizers, gcFinalizer),
				"resourceVersion": backup.ResourceVersion,
			},
		}

		patchBytes, err := json.Marshal(patch)
		if err != nil {
			log.WithError(errors.WithStack(err)).Error("error marshaling finalizers patch")
			continue
		}

		_, err = s.arkClient.ArkV1().Backups(backup.Namespace).Patch(backup.Name, types.MergePatchType, patchBytes)
		if err != nil {
			log.WithError(errors.WithStack(err)).Error("error marshaling finalizers patch")
		}
	}
}

func newBackupper(
	discoveryHelper arkdiscovery.Helper,
	clientPool dynamic.ClientPool,
	backupService cloudprovider.BackupService,
	snapshotService cloudprovider.SnapshotService,
	kubeClientConfig *rest.Config,
	kubeCoreV1Client kcorev1client.CoreV1Interface,
	namespace string,
	resticManager restic.RepositoryManager,
) (backup.Backupper, error) {
	return backup.NewKubernetesBackupper(
		discoveryHelper,
		client.NewDynamicFactory(clientPool),
		backup.NewPodCommandExecutor(kubeClientConfig, kubeCoreV1Client.RESTClient()),
		snapshotService,
		kubeCoreV1Client.Pods(namespace),
		kubeCoreV1Client, // PersistentVolumeClaimsGetter
		resticManager,
	)
}

func newRestorer(
	discoveryHelper arkdiscovery.Helper,
	clientPool dynamic.ClientPool,
	backupService cloudprovider.BackupService,
	snapshotService cloudprovider.SnapshotService,
	resourcePriorities []string,
	backupClient arkv1client.BackupsGetter,
	kubeClient kubernetes.Interface,
	logger logrus.FieldLogger,
) (restore.Restorer, error) {
	return restore.NewKubernetesRestorer(
		discoveryHelper,
		client.NewDynamicFactory(clientPool),
		backupService,
		snapshotService,
		resourcePriorities,
		backupClient,
		kubeClient.CoreV1().Namespaces(),
		logger,
	)
}
