# DBaaS Operator

This set of controllers is meant to be used as a replacement for the ansible service broker and https://github.com/amazeeio/dbaas-mariadb-apb to run in Kubernetes.

It allows for provisiong and deprovisioning of shared MySQL/MariaDB, PostgreSQL, and MongoDB databases.

## Components
### MariaDB/MySQL
* MariaDBProvider - These contain the core connection details for a specific provider (AWS RDS, Azure DB for MySQL, etc)
* MariaDBConsumer - These contain the specific connection details for a consumer, multiple consumers can be defined in the one namespace and each will get their own specific service endpoints created pointing to the provider.

### PostgreSQL
* PostgreSQLProvider - These contain the core connection details for a specific provider (AWS RDS primarily or generic postgresql, other cloud providers currently untested)
* PostgreSQLConsumer - These contain the specific connection details for a consumer, multiple consumers can be defined in the one namespace and each will get their own specific service endpoints created pointing to the provider.

### MongoDB
* MongoDBProvider - These contain the core connection details for a specific provider (Works with AWS DocumentDB, or generic MongoDB with authentication)
* MongoDBConsumer - These contain the specific connection details for a consumer, multiple consumers can be defined in the one namespace and each will get their own specific service endpoints created pointing to the provider.

## Test It Out
This will spin up a local mysql, postgresql, and mongodb provider. A kind cluster is started, and the operator is installed, then some basic tests are performed to confirm the providers and consumer provisioning and deprovisioning is successful.

### Running the tests directly
```
make operator-test
# if at anypoint you need to clean up
make clean
```

## Code references

* Most of the logic for the controllers located in `controllers/<mariadb/postgres/mongodb>/`
* Spec definitions are in `api/<mariadb/postgres/mongodb>/v*/*_types.go`

## Config samples

* located in `config/samples`

# Developing
## Install Kubebuilder
```
os=$(go env GOOS)
arch=$(go env GOARCH)

# download kubebuilder and extract it to tmp
curl -L https://github.com/kubernetes-sigs/kubebuilder/releases/download/v2.2.0/kubebuilder_2.2.0_${os}_${arch}.tar.gz | tar -xz -C /tmp/

# move to a long-term location and put it on your path
# (you'll need to set the KUBEBUILDER_ASSETS env var if you put it somewhere else)
sudo mv /tmp/kubebuilder_2.2.0_${os}_${arch} /usr/local/kubebuilder
export PATH=$PATH:/usr/local/kubebuilder/bin
```
