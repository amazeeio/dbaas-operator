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

// MongoDBConsumerSpec defines the desired state of MongoDBConsumer
type MongoDBConsumerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// These are the spec options for consumers
	Environment string                  `json:"environment,omitempty"`
	Provider    MongoDBConsumerProvider `json:"provider,omitempty"`
	Consumer    MongoDBConsumerData     `json:"consumer,omitempty"`
}

// MongoDBConsumerData defines the provider link for this consumer
type MongoDBConsumerData struct {
	Database string                  `json:"database,omitempty"`
	Password string                  `json:"password,omitempty"`
	Username string                  `json:"username,omitempty"`
	Services MongoDBConsumerServices `json:"services,omitempty"`
}

// MongoDBConsumerServices defines the provider link for this consumer
type MongoDBConsumerServices struct {
	Primary string `json:"primary,omitempty"`
}

// MongoDBConsumerProvider defines the provider link for this consumer
type MongoDBConsumerProvider struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	Port      string `json:"port,omitempty"`
}

// MongoDBConsumerStatus defines the observed state of MongoDBConsumer
type MongoDBConsumerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true

// MongoDBConsumer is the Schema for the mongodbconsumers API
type MongoDBConsumer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBConsumerSpec   `json:"spec,omitempty"`
	Status MongoDBConsumerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBConsumerList contains a list of MongoDBConsumer
type MongoDBConsumerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBConsumer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBConsumer{}, &MongoDBConsumerList{})
}