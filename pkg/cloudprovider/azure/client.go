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

package azure

import (
	"io/ioutil"
	"os"

	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/ghodss/yaml"
	"github.com/heptio/ark/pkg/cloudprovider"
	"github.com/pkg/errors"
)

type clientConfig struct {
	SubscriptionID   string `json:"subscription_id"`
	TenantID         string `json:"tenant_id"`
	ClientID         string `json:"client_id"`
	ClientSecret     string `json:"client_secret"`
	StorageAccountID string `json:"storage_account_id"`
	StorageKey       string `json:"storage_key"`
	ResourceGroup    string `json:"resource_group"`
}

const credentialsEnvVar = "AZURE_CREDENTIALS_FILE"

func loadConfig() (clientConfig, error) {
	credentialsFile := os.Getenv(credentialsEnvVar)
	if credentialsFile == "" {
		credentialsFile = cloudprovider.DefaultSharedCredentialsFile
	}

	f, err := os.Open(credentialsFile)
	if err != nil {
		return clientConfig{}, errors.Wrap(err, "error opening credentials file")
	}
	defer f.Close()

	contents, err := ioutil.ReadAll(f)
	if err != nil {
		return clientConfig{}, errors.Wrap(err, "error reading credentials file")
	}

	return loadConfigFromBytes(contents)
}

func loadConfigFromBytes(contents []byte) (clientConfig, error) {
	var c clientConfig
	if err := yaml.Unmarshal(contents, &c); err != nil {
		return clientConfig{}, errors.Wrap(err, "error unmarshalling config")
	}

	return c, nil
}

func getServicePrincipalToken(tenantID, clientID, clientSecret, scope string) (*adal.ServicePrincipalToken, error) {
	oauthConfig, err := adal.NewOAuthConfig(azure.PublicCloud.ActiveDirectoryEndpoint, tenantID)
	if err != nil {
		return nil, err
	}

	return adal.NewServicePrincipalToken(*oauthConfig, clientID, clientSecret, scope)
}
