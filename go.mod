module github.com/amazeeio/dbaas-operator

go 1.16

require (
	github.com/go-logr/logr v0.4.0
	github.com/go-sql-driver/mysql v1.5.0
	github.com/google/uuid v1.1.1
	github.com/gorilla/mux v1.8.0
	github.com/lib/pq v1.3.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.14.0
	go.mongodb.org/mongo-driver v1.4.4
	k8s.io/api v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/client-go v0.21.3
	sigs.k8s.io/controller-runtime v0.9.6
)
