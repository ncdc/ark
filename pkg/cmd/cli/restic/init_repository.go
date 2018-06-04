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

package restic

import (
	"crypto/rand"
	"io/ioutil"

	"github.com/heptio/ark/pkg/client"
	"github.com/heptio/ark/pkg/cmd"
	"github.com/heptio/ark/pkg/restic"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kclientset "k8s.io/client-go/kubernetes"
)

func NewInitRepositoryCommand(f client.Factory) *cobra.Command {
	o := NewInitRepositoryOptions()

	c := &cobra.Command{
		Use:   "init-repository",
		Short: "TODO",
		Long:  "TODO",
		Run: func(c *cobra.Command, args []string) {
			cmd.CheckError(o.Complete(f))
			cmd.CheckError(o.Validate(f))
			cmd.CheckError(o.Run(f))
		},
	}

	o.BindFlags(c.Flags())

	return c
}

type InitRepositoryOptions struct {
	Namespace string
	KeyFile   string
	KeyData   string
	KeySize   int

	kubeClient kclientset.Interface
	keyBytes   []byte
}

func NewInitRepositoryOptions() *InitRepositoryOptions {
	return &InitRepositoryOptions{
		KeySize: 1024,
	}
}

func (o *InitRepositoryOptions) BindFlags(flags *pflag.FlagSet) {
	flags.StringVar(&o.KeyFile, "key-file", o.KeyFile, "Path to file containing the encryption key for the restic repository. Optional; if unset, Ark will generate a random key for you.")
	flags.StringVar(&o.KeyData, "key-data", o.KeyData, "Encryption key for the restic repository. Optional; if unset, Ark will generate a random key for you.")
	flags.IntVar(&o.KeySize, "key-size", o.KeySize, "Size of the generated key for the restic repository")
}

func (o *InitRepositoryOptions) Complete(f client.Factory) error {
	if o.KeyFile != "" && o.KeyData != "" {
		return errors.Errorf("only one of --key-file and --key-data may be specified")
	}

	if o.KeyFile == "" && o.KeyData == "" && o.KeySize < 1 {
		return errors.Errorf("--key-size must be at least 1")
	}

	o.Namespace = f.Namespace()

	if o.KeyFile != "" {
		data, err := ioutil.ReadFile(o.KeyFile)
		if err != nil {
			return err
		}
		o.keyBytes = data
	}

	if len(o.KeyData) == 0 {
		o.keyBytes = make([]byte, o.KeySize)
		// rand.Reader always returns a nil error
		_, _ = rand.Read(o.keyBytes)
	}

	return nil
}

func (o *InitRepositoryOptions) Validate(f client.Factory) error {
	if len(o.keyBytes) == 0 {
		return errors.Errorf("keyBytes is required")
	}

	kubeClient, err := f.KubeClient()
	if err != nil {
		return err
	}
	o.kubeClient = kubeClient

	if _, err := kubeClient.CoreV1().Namespaces().Get(o.Namespace, metav1.GetOptions{}); err != nil {
		return err
	}

	return nil
}

func (o *InitRepositoryOptions) Run(f client.Factory) error {
	return restic.NewRepositoryKey(o.kubeClient.CoreV1(), o.Namespace, o.keyBytes)
}
