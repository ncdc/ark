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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromBytes(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expected      clientConfig
		expectedError string
	}{
		{
			name:     "empty file",
			input:    "",
			expected: clientConfig{},
		},
		{
			name:          "invalid file",
			input:         "asdf,23423@;oai",
			expectedError: "foo",
		},
		{
			name: "full file",
			input: `client_id: id
client_secret: secret
subscription_id: sub
tenant_id: tenant
storage_account_id: storage
storage_key: key
resource_group: group`,
			expected: clientConfig{
				ClientID:         "id",
				ClientSecret:     "secret",
				SubscriptionID:   "sub",
				TenantID:         "tenant",
				StorageAccountID: "storage",
				StorageKey:       "key",
				ResourceGroup:    "group",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := loadConfigFromBytes([]byte(test.input))
			if test.expectedError != "" {
				assert.Error(t, err)
				assert.Regexp(t, "error unmarshalling config: error unmarshaling JSON", err.Error())
				return
			}

			require.NoError(t, err)

			assert.Equal(t, test.expected, actual)
		})
	}
}
