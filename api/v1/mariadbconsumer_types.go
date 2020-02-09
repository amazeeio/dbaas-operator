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

// MariaDBConsumerSpec defines the desired state of MariaDBConsumer
type MariaDBConsumerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// These are the spec options for consumers
	Environment string                  `json:"environment,omitempty"`
	Provider    MariaDBConsumerProvider `json:"provider,omitempty"`
	Consumer    MariaDBConsumerData     `json:"consumer,omitempty"`
}

// MariaDBConsumerData defines the provider link for this consumer
type MariaDBConsumerData struct {
	Database string                  `json:"database,omitempty"`
	Password string                  `json:"password,omitempty"`
	Username string                  `json:"username,omitempty"`
	Services MariaDBConsumerServices `json:"services,omitempty"`
}

// MariaDBConsumerServices defines the provider link for this consumer
type MariaDBConsumerServices struct {
	Primary  string   `json:"primary,omitempty"`
	Replicas []string `json:"replicas,omitempty"`
}

// MariaDBConsumerProvider defines the provider link for this consumer
type MariaDBConsumerProvider struct {
	Name                 string   `json:"name,omitempty"`
	Namespace            string   `json:"namespace,omitempty"`
	Hostname             string   `json:"hostname,omitempty"`
	ReadReplicaHostnames []string `json:"readReplicas,omitempty"`
	Port                 string   `json:"port,omitempty"`
}

// MariaDBConsumerStatus defines the observed state of MariaDBConsumer
type MariaDBConsumerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true

// MariaDBConsumer is the Schema for the mariadbconsumers API
type MariaDBConsumer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MariaDBConsumerSpec   `json:"spec,omitempty"`
	Status MariaDBConsumerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MariaDBConsumerList contains a list of MariaDBConsumer
type MariaDBConsumerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MariaDBConsumer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MariaDBConsumer{}, &MariaDBConsumerList{})
}
