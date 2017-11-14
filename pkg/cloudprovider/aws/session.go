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

package aws

import (
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/heptio/ark/pkg/cloudprovider"
	"github.com/pkg/errors"
)

const (
	credentialsEnvVar = "AWS_SHARED_CREDENTIALS_FILE"
)

func getSession(config *aws.Config) (*session.Session, error) {
	if os.Getenv(credentialsEnvVar) == "" {
		os.Setenv(credentialsEnvVar, cloudprovider.DefaultSharedCredentialsFile)
	}

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if _, err := sess.Config.Credentials.Get(); err != nil {
		return nil, errors.WithStack(err)
	}

	return sess, nil
}
