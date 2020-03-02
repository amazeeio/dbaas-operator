/*

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

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PostgreSQLProviderSpec defines the desired state of PostgreSQLProvider
type PostgreSQLProviderSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// These are the spec options for providers
	Environment string `json:"environment,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Password    string `json:"password,omitempty"`
	Port        string `json:"port,omitempty"`
	Username    string `json:"user,omitempty"`
	Name        string `json:"name,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Type        string `json:"type,omitempty"`
}

// PostgreSQLProviderStatus defines the observed state of PostgreSQLProvider
type PostgreSQLProviderStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true

// PostgreSQLProvider is the Schema for the postgresqlproviders API
type PostgreSQLProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgreSQLProviderSpec   `json:"spec,omitempty"`
	Status PostgreSQLProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgreSQLProviderList contains a list of PostgreSQLProvider
type PostgreSQLProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgreSQLProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgreSQLProvider{}, &PostgreSQLProviderList{})
}
