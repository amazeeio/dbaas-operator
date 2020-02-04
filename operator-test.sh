#!/bin/bash

#KIND_VER=v1.13.12
#KIND_VER=v1.14.10
#KIND_VER=v1.15.7
#KIND_VER=v1.16.4
KIND_VER=v1.17.0
# or get the latest tagged version of a specific k8s version of kind
#KIND_VER=$(curl -s https://hub.docker.com/v2/repositories/kindest/node/tags | jq -r '.results | .[].name' | grep 'v1.17' | sort -Vr | head -1)
KIND_NAME=dbaas-operator-test
OPERATOR_IMAGE=amazeeio/dbaas-operator:test-tag
CHECK_TIMEOUT=60

check_operator_log () {
  echo "=========== OPERATOR LOG ============"
  kubectl logs $(kubectl get pods  -n dbaas-operator-system --no-headers | awk '{print $1}') -c manager -n dbaas-operator-system
}

tear_down () {
  echo "============= TEAR DOWN ============="
  kind delete cluster --name ${KIND_NAME}
  docker-compose down
}

start_up () {
  echo "================ BEGIN ================"
  echo "==> Bring up local provider"
  docker-compose up -d
  CHECK_COUNTER=1
  echo "==> Ensure database provider is running"
  until $(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e 'show databases;' | grep -q "information_schema")
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database provider not running yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database provider startup reached"
    exit 1
  fi
  done
}

start_kind () {
  echo "==> Start kind ${KIND_VER}" 
  kind create cluster --image kindest/node:${KIND_VER} --name ${KIND_NAME}
  kubectl cluster-info --context kind-${KIND_NAME}

  echo "==> Switch kube context to kind" 
  kubectl config use-context kind-${KIND_NAME}
}

build_deploy_operator () {
  echo "==> Build and deploy operator"
  make docker-build IMG=${OPERATOR_IMAGE}
  kind load docker-image ${OPERATOR_IMAGE} --name ${KIND_NAME}
  make deploy IMG=${OPERATOR_IMAGE}

  CHECK_COUNTER=1
  echo "==> Ensure operator is running"
  until $(kubectl get pods  -n dbaas-operator-system --no-headers | grep -q "Running")
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Operator not running yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for operator startup reached"
    check_operator_log
    exit 1
  fi
  done
}

check_services () {
  PRIMARY=$(kubectl get mariadbconsumer/$1 -o json | jq -r '.spec.consumer.services.primary')
  echo "====> Check primary service ${PRIMARY}"
  kubectl get service/${PRIMARY} -o yaml
  REPLICAS=$(kubectl get mariadbconsumer/$1 -o json | jq -r '.spec.consumer.services.replicas | .[]')
  for REPLICA in ${REPLICAS}
  do
  echo "====> Check replica service ${REPLICA}"
    kubectl get service/${REPLICA} -o yaml
  done
}

add_delete_consumer () {
  echo "====> Add a consumer"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until kubectl get mariadbconsumer/$2 -o json | jq -e '.spec.consumer.database?'
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database not created yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database creation reached"
    check_operator_log
    exit 1
  fi
  done
  echo "====> Get MariaDBConsumer"
  kubectl get mariadbconsumer/$2 -o yaml
  DB_NAME=$(kubectl get mariadbconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo "==> Check if the operator creates the database"
  DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_EXISTS} exists"
  else 
    echo "database ${DB_EXISTS} does not exist"
    check_operator_log
    exit 1
  fi

  echo "==> Check services"
  check_services $2

  echo "==> Delete the consumer"
  kubectl delete -f $1
  echo "==> Check if the operator deletes the database"
  DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_EXISTS} exists"
    check_operator_log
    exit 1
  else 
    echo "database ${DB_EXISTS} does not exist"
  fi
}

start_up
start_kind
build_deploy_operator

echo "==> Add a provider"
kubectl apply -f test-resources/provider.yaml

echo "==> Test blank consumer"
add_delete_consumer test-resources/consumer.yaml mariadbconsumer-testing
 
echo "==> Test seeded consumer"
docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb; CREATE USER IF NOT EXISTS testdb@'%' IDENTIFIED BY 'testdb'; GRANT ALL ON testdb.* TO testdb@'%'; FLUSH PRIVILEGES;"
add_delete_consumer test-resources/consumer-test.yaml mariadbconsumer-testing-2

echo "==> Test seeded consumer V2"
docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb1; CREATE USER IF NOT EXISTS testdb1@'%' IDENTIFIED BY 'testdb1'; GRANT ALL ON testdb1.* TO testdb1@'%'; FLUSH PRIVILEGES;"
add_delete_consumer test-resources/consumer-test-2.yaml mariadbconsumer-testing-3

check_operator_log
tear_down
echo "================ END ================"