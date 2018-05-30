/*
Copyright 2018 the Heptio Ark contributors.

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

package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	corev1api "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corev1informers "k8s.io/client-go/informers/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	arkv1api "github.com/heptio/ark/pkg/apis/ark/v1"
	arkv1client "github.com/heptio/ark/pkg/generated/clientset/versioned/typed/ark/v1"
	informers "github.com/heptio/ark/pkg/generated/informers/externalversions/ark/v1"
	listers "github.com/heptio/ark/pkg/generated/listers/ark/v1"
	"github.com/heptio/ark/pkg/restic"
	"github.com/heptio/ark/pkg/util/boolptr"
	"github.com/heptio/ark/pkg/util/kube"
)

type podVolumeRestoreController struct {
	*genericController

	podVolumeRestoreClient arkv1client.PodVolumeRestoresGetter
	podVolumeRestoreLister listers.PodVolumeRestoreLister
	secretLister           corev1listers.SecretLister
	podLister              corev1listers.PodLister
	pvcLister              corev1listers.PersistentVolumeClaimLister
	nodeName               string

	processRestoreFunc func(*arkv1api.PodVolumeRestore) error
}

// NewPodVolumeRestoreController creates a new pod volume restore controller.
func NewPodVolumeRestoreController(
	logger logrus.FieldLogger,
	podVolumeRestoreInformer informers.PodVolumeRestoreInformer,
	podVolumeRestoreClient arkv1client.PodVolumeRestoresGetter,
	podInformer cache.SharedIndexInformer,
	secretInformer corev1informers.SecretInformer,
	pvcInformer corev1informers.PersistentVolumeClaimInformer,
	nodeName string,
) Interface {
	c := &podVolumeRestoreController{
		genericController:      newGenericController("pod-volume-restore", logger),
		podVolumeRestoreClient: podVolumeRestoreClient,
		podVolumeRestoreLister: podVolumeRestoreInformer.Lister(),
		podLister:              corev1listers.NewPodLister(podInformer.GetIndexer()),
		secretLister:           secretInformer.Lister(),
		pvcLister:              pvcInformer.Lister(),
		nodeName:               nodeName,
	}

	c.syncHandler = c.processQueueItem
	c.cacheSyncWaiters = append(
		c.cacheSyncWaiters,
		podVolumeRestoreInformer.Informer().HasSynced,
		secretInformer.Informer().HasSynced,
		podInformer.HasSynced,
		pvcInformer.Informer().HasSynced,
	)
	c.processRestoreFunc = c.processRestore

	podVolumeRestoreInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				pvr := obj.(*arkv1api.PodVolumeRestore)
				log := c.logger.WithField("key", kube.NamespaceAndName(pvr))

				if !shouldEnqueuePVR(pvr, c.podLister, c.nodeName, log) {
					return
				}

				log.Debug("enqueueing")
				c.enqueue(obj)
			},
			UpdateFunc: func(_, obj interface{}) {
				pvr := obj.(*arkv1api.PodVolumeRestore)
				log := c.logger.WithField("key", kube.NamespaceAndName(pvr))

				if !shouldEnqueuePVR(pvr, c.podLister, c.nodeName, log) {
					return
				}

				log.Debug("enqueueing")
				c.enqueue(obj)
			},
		},
	)

	podInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				pod := obj.(*corev1api.Pod)
				log := c.logger.WithField("key", kube.NamespaceAndName(pod))

				for _, pvr := range pvrsToEnqueueForPod(pod, c.podVolumeRestoreLister, c.nodeName, log) {
					c.enqueue(pvr)
				}
			},
			UpdateFunc: func(_, obj interface{}) {
				pod := obj.(*corev1api.Pod)
				log := c.logger.WithField("key", kube.NamespaceAndName(pod))

				for _, pvr := range pvrsToEnqueueForPod(pod, c.podVolumeRestoreLister, c.nodeName, log) {
					c.enqueue(pvr)
				}
			},
		},
	)

	c.resyncPeriod = time.Hour
	// c.resyncFunc = c.deleteExpiredRequests

	return c
}

func shouldProcessPod(pod *corev1api.Pod, nodeName string, log logrus.FieldLogger) bool {
	// if the pod lister being used is filtered to pods on this node, this is superfluous
	if pod.Spec.NodeName != nodeName {
		log.Debugf("Pod is scheduled on node %s, not enqueueing.", pod.Spec.NodeName)
		return false
	}

	// only process items for pods that have the restic initContainer running
	if !isPodWaiting(pod) {
		log.Debugf("Pod is not running restic initContainer, not enqueueing.")
		return false
	}

	return true
}

func shouldProcessPVR(pvr *arkv1api.PodVolumeRestore, log logrus.FieldLogger) bool {
	// only process new items
	if pvr.Status.Phase != "" && pvr.Status.Phase != arkv1api.PodVolumeRestorePhaseNew {
		log.Debugf("Item has phase %s, not enqueueing.", pvr.Status.Phase)
		return false
	}

	return true
}

func pvrsToEnqueueForPod(pod *corev1api.Pod, pvrLister listers.PodVolumeRestoreLister, nodeName string, log logrus.FieldLogger) []*arkv1api.PodVolumeRestore {
	if !shouldProcessPod(pod, nodeName, log) {
		return nil
	}

	selector, err := labels.Parse(fmt.Sprintf("%s=%s", arkv1api.PodUIDLabel, pod.UID))
	if err != nil {
		log.WithError(err).Error("Unable to parse label selector %s", fmt.Sprintf("%s=%s", arkv1api.PodUIDLabel, pod.UID))
		return nil
	}

	pvrs, err := pvrLister.List(selector)
	if err != nil {
		log.WithError(err).Error("Unable to list pod volume restores")
		return nil
	}

	var res []*arkv1api.PodVolumeRestore
	for i, pvr := range pvrs {
		if shouldProcessPVR(pvr, log) {
			res = append(res, pvrs[i])
		}
	}

	return res
}

func shouldEnqueuePVR(pvr *arkv1api.PodVolumeRestore, podLister corev1listers.PodLister, nodeName string, log logrus.FieldLogger) bool {
	if !shouldProcessPVR(pvr, log) {
		return false
	}

	pod, err := podLister.Pods(pvr.Spec.Pod.Namespace).Get(pvr.Spec.Pod.Name)
	if err != nil {
		log.WithError(err).Errorf("Unable to get item's pod %s/%s, not enqueueing.", pvr.Spec.Pod.Namespace, pvr.Spec.Pod.Name)
		return false
	}

	if !shouldProcessPod(pod, nodeName, log) {
		return false
	}

	return true
}

func isPodWaiting(pod *corev1api.Pod) bool {
	return len(pod.Spec.InitContainers) == 0 ||
		pod.Spec.InitContainers[0].Name != restic.InitContainer ||
		len(pod.Status.InitContainerStatuses) == 0 ||
		pod.Status.InitContainerStatuses[0].State.Running == nil
}

func (c *podVolumeRestoreController) processQueueItem(key string) error {
	log := c.logger.WithField("key", key)
	log.Debug("Running processItem")

	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return errors.Wrap(err, "error splitting queue key")
	}

	req, err := c.podVolumeRestoreLister.PodVolumeRestores(ns).Get(name)
	if apierrors.IsNotFound(err) {
		log.Debug("Unable to find PodVolumeRestore")
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "error getting PodVolumeRestore")
	}

	// Don't mutate the shared cache
	reqCopy := req.DeepCopy()
	return c.processRestoreFunc(reqCopy)
}

func (c *podVolumeRestoreController) processRestore(req *arkv1api.PodVolumeRestore) error {
	log := c.logger.WithFields(logrus.Fields{
		"namespace": req.Namespace,
		"name":      req.Name,
	})

	var err error

	// update status to InProgress
	req, err = c.patchPodVolumeRestore(req, updatePodVolumeRestorePhaseFunc(arkv1api.PodVolumeRestorePhaseInProgress))
	if err != nil {
		log.WithError(err).Error("Error setting phase to InProgress")
		return errors.WithStack(err)
	}

	pod, err := c.podLister.Pods(req.Spec.Pod.Namespace).Get(req.Spec.Pod.Name)
	if err != nil {
		log.WithError(err).Errorf("Error getting pod %s/%s", req.Spec.Pod.Namespace, req.Spec.Pod.Name)
		return c.fail(req, errors.Wrap(err, "error getting pod").Error(), log)
	}

	volumeDir, err := kube.GetVolumeDirectory(pod, req.Spec.Volume, c.pvcLister)
	if err != nil {
		log.WithError(err).Error("Error getting volume directory name")
		return c.fail(req, errors.Wrap(err, "error getting volume directory name").Error(), log)
	}

	// temp creds
	file, err := restic.TempCredentialsFile(c.secretLister, req.Spec.Pod.Namespace)
	if err != nil {
		log.WithError(err).Error("Error creating temp restic credentials file")
		return c.fail(req, errors.Wrap(err, "error creating temp restic credentials file").Error(), log)
	}
	// ignore error since there's nothing we can do and it's a temp file.
	defer os.Remove(file)

	resticCmd := restic.RestoreCommand(
		req.Spec.RepoPrefix,
		req.Spec.Pod.Namespace,
		file,
		string(req.Spec.Pod.UID),
		req.Spec.SnapshotID,
	)

	output, err := resticCmd.Cmd().Output()
	log.Debugf("Ran command=%s, stdout=%s", resticCmd.String(), output)
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		log.WithError(err).Errorf("Error running command=%s, stdout=%s, stderr=%s", resticCmd.String(), output, stderr)

		return c.fail(req, fmt.Sprintf("error running restic restore, stderr=%s: %s", stderr, err.Error()), log)
	}

	var restoreUID types.UID
	for _, owner := range req.OwnerReferences {
		if boolptr.IsSetToTrue(owner.Controller) {
			restoreUID = owner.UID
			break
		}
	}

	cmd := exec.Command("/bin/sh", "-c", strings.Join([]string{"/complete-restore.sh", string(req.Spec.Pod.UID), volumeDir, string(restoreUID)}, " "))
	output, err = cmd.Output()
	log.Debugf("Ran command=%s, stdout=%s", cmd.Args, output)
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		log.WithError(err).Errorf("Error running command=%s, stdout=%s, stderr=%s", cmd.Args, output, stderr)

		return c.fail(req, fmt.Sprintf("error running restic restore: %s: stderr=%s", err.Error(), stderr), log)
	}

	// update status to Completed
	if _, err = c.patchPodVolumeRestore(req, updatePodVolumeRestorePhaseFunc(arkv1api.PodVolumeRestorePhaseCompleted)); err != nil {
		log.WithError(err).Error("Error setting phase to Completed")
		return err
	}

	return nil
}

func (c *podVolumeRestoreController) patchPodVolumeRestore(req *arkv1api.PodVolumeRestore, mutate func(*arkv1api.PodVolumeRestore)) (*arkv1api.PodVolumeRestore, error) {
	// Record original json
	oldData, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Wrap(err, "error marshalling original PodVolumeRestore")
	}

	// Mutate
	mutate(req)

	// Record new json
	newData, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Wrap(err, "error marshalling updated PodVolumeRestore")
	}

	patchBytes, err := jsonpatch.CreateMergePatch(oldData, newData)
	if err != nil {
		return nil, errors.Wrap(err, "error creating json merge patch for PodVolumeRestore")
	}

	req, err = c.podVolumeRestoreClient.PodVolumeRestores(req.Namespace).Patch(req.Name, types.MergePatchType, patchBytes)
	if err != nil {
		return nil, errors.Wrap(err, "error patching PodVolumeRestore")
	}

	return req, nil
}

func (c *podVolumeRestoreController) fail(req *arkv1api.PodVolumeRestore, msg string, log logrus.FieldLogger) error {
	if _, err := c.patchPodVolumeRestore(req, func(pvr *arkv1api.PodVolumeRestore) {
		pvr.Status.Phase = arkv1api.PodVolumeRestorePhaseFailed
		pvr.Status.Message = msg
	}); err != nil {
		log.WithError(err).Error("Error setting phase to Failed")
		return err
	}
	return nil
}

func updatePodVolumeRestorePhaseFunc(phase arkv1api.PodVolumeRestorePhase) func(r *arkv1api.PodVolumeRestore) {
	return func(r *arkv1api.PodVolumeRestore) {
		r.Status.Phase = phase
	}
}
