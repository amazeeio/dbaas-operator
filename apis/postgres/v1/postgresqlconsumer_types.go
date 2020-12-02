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

// PostgreSQLConsumerSpec defines the desired state of PostgreSQLConsumer
type PostgreSQLConsumerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// These are the spec options for consumers
	Environment string                     `json:"environment,omitempty"`
	Provider    PostgreSQLConsumerProvider `json:"provider,omitempty"`
	Consumer    PostgreSQLConsumerData     `json:"consumer,omitempty"`
}

// PostgreSQLConsumerData defines the provider link for this consumer
type PostgreSQLConsumerData struct {
	Database string                     `json:"database,omitempty"`
	Password string                     `json:"password,omitempty"`
	Username string                     `json:"username,omitempty"`
	Services PostgreSQLConsumerServices `json:"services,omitempty"`
}

// PostgreSQLConsumerServices defines the provider link for this consumer
type PostgreSQLConsumerServices struct {
	Primary string `json:"primary,omitempty"`
}

// PostgreSQLConsumerProvider defines the provider link for this consumer
type PostgreSQLConsumerProvider struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	Port      string `json:"port,omitempty"`
	Type      string `json:"type,omitempty"`
}

// PostgreSQLConsumerStatus defines the observed state of PostgreSQLConsumer
type PostgreSQLConsumerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true

// PostgreSQLConsumer is the Schema for the postgresqlconsumers API
type PostgreSQLConsumer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgreSQLConsumerSpec   `json:"spec,omitempty"`
	Status PostgreSQLConsumerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgreSQLConsumerList contains a list of PostgreSQLConsumer
type PostgreSQLConsumerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgreSQLConsumer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgreSQLConsumer{}, &PostgreSQLConsumerList{})
}
