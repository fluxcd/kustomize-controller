/*
Copyright 2025 The Flux authors

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

package v1

const (
	// MetricFetchKubeConfig is the metric name for counting
	// kubeconfig fetch attempts.
	MetricFetchKubeConfig = "fetch_kubeconfig"

	// MetricDecryptWithAWS is the metric name for counting
	// decryption attempts using AWS KMS.
	MetricDecryptWithAWS = "decrypt_with_aws"

	// MetricDecryptWithAzure is the metric name for counting
	// decryption attempts using Azure Key Vault.
	MetricDecryptWithAzure = "decrypt_with_azure"

	// MetricDecryptWithGCP is the metric name for counting
	// decryption attempts using GCP KMS.
	MetricDecryptWithGCP = "decrypt_with_gcp"
)

// AllMetrics is the list of all supported cache metrics.
var AllMetrics = []string{
	MetricFetchKubeConfig,
	MetricDecryptWithAWS,
	MetricDecryptWithAzure,
	MetricDecryptWithGCP,
}
