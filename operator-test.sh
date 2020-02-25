#!/bin/bash

NOCOLOR='\033[0m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'

#KIND_VER=v1.13.12
#KIND_VER=v1.14.10
#KIND_VER=v1.15.7
#KIND_VER=v1.16.4
KIND_VER=v1.17.0
# or get the latest tagged version of a specific k8s version of kind
#KIND_VER=$(curl -s https://hub.docker.com/v2/repositories/kindest/node/tags | jq -r '.results | .[].name' | grep 'v1.17' | sort -Vr | head -1)
KIND_NAME=dbaas-operator-test
OPERATOR_IMAGE=amazeeio/dbaas-operator:test-tag
CHECK_TIMEOUT=10

check_operator_log () {
  echo -e "${GREEN}========= FULL OPERATOR LOG =========${NOCOLOR}"
  kubectl logs $(kubectl get pods  -n dbaas-operator-system --no-headers | awk '{print $1}') -c manager -n dbaas-operator-system
}

tear_down () {
  echo -e "${GREEN}============= TEAR DOWN =============${NOCOLOR}"
  kind delete cluster --name ${KIND_NAME}
  docker-compose down
}

start_up () {
  echo -e "${GREEN}================ BEGIN ================${NOCOLOR}"
  echo -e "${GREEN}==>${NOCOLOR} Bring up local provider"
  docker-compose up -d
  CHECK_COUNTER=1
  echo -e "${GREEN}==>${NOCOLOR} Ensure database provider is running"
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
  echo -e "${GREEN}==>${NOCOLOR} Ensure multi database provider is running"
  until $(docker-compose exec -T mysql mysql --host=local-dbaas-provider-multi --port=3306 -uroot -e 'show databases;' | grep -q "information_schema")
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Multi database provider not running yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for multi database provider startup reached"
    exit 1
  fi
  done
}

start_kind () {
  echo -e "${GREEN}==>${NOCOLOR} Start kind ${KIND_VER}" 
  kind create cluster --image kindest/node:${KIND_VER} --name ${KIND_NAME}
  kubectl cluster-info --context kind-${KIND_NAME}

  echo -e "${GREEN}==>${NOCOLOR} Switch kube context to kind" 
  kubectl config use-context kind-${KIND_NAME}
}

build_deploy_operator () {
  echo -e "${GREEN}==>${NOCOLOR} Build and deploy operator"
  make docker-build IMG=${OPERATOR_IMAGE}
  kind load docker-image ${OPERATOR_IMAGE} --name ${KIND_NAME}
  make deploy IMG=${OPERATOR_IMAGE}

  CHECK_COUNTER=1
  echo -e "${GREEN}==>${NOCOLOR} Ensure operator is running"
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
  echo -e "${GREEN}====>${NOCOLOR} Check primary service ${PRIMARY}"
  kubectl get service/${PRIMARY} -o yaml
  REPLICAS=$(kubectl get mariadbconsumer/$1 -o json | jq -r '.spec.consumer.services.replicas | .[]')
  for REPLICA in ${REPLICAS}
  do
  echo -e "${GREEN}====>${NOCOLOR} Check replica service ${REPLICA}"
    kubectl get service/${REPLICA} -o yaml
  done
}

add_delete_consumer () {
  echo -e "${GREEN}====>${NOCOLOR} Add a consumer"
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
  echo -e "${GREEN}====>${NOCOLOR} Get MariaDBConsumer"
  kubectl get mariadbconsumer/$2 -o yaml
  DB_NAME=$(kubectl get mariadbconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo -e "${GREEN}==>${NOCOLOR} Check if the operator creates the database"
  DB_EXISTS=$(docker-compose exec -T mysql mysql --host=${3:-local-dbaas-provider} --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_NAME} exists"
  else 
    echo "database ${DB_NAME} does not exist"
    check_operator_log
    exit 1
  fi

  echo -e "${GREEN}==>${NOCOLOR} Check services"
  check_services $2

  echo -e "${GREEN}==>${NOCOLOR} Delete the consumer"
  kubectl delete -f $1
  echo -e "${GREEN}==>${NOCOLOR} Check if the operator deletes the database"
  DB_EXISTS=$(docker-compose exec -T mysql mysql --host=${3:-local-dbaas-provider} --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_NAME} exists"
    check_operator_log
    exit 1
  else 
    echo "database ${DB_NAME} does not exist"
  fi
}

start_up
start_kind
build_deploy_operator

echo -e "${GREEN}==>${NOCOLOR} Add a provider"
kubectl apply -f test-resources/provider.yaml

echo -e "${GREEN}====>${NOCOLOR} Test blank consumer"
echo "Test adding a blank consumer with a specific environment type."
echo "This test should create the database and user, and the associated services randomly"
add_delete_consumer test-resources/consumer.yaml mariadbconsumer-testing
echo -e "${YELLOW}====>${NOCOLOR} Blank consumer logs"
check_operator_log | grep mariadbconsumer-testing-testing

echo -e "${GREEN}====>${NOCOLOR} Test seeded consumer"
echo "Test adding a seeded consumer with a specific environment type."
echo "This test already has pre-seeded database username and password, but will create the associated services"
docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb; CREATE USER IF NOT EXISTS testdb@'%' IDENTIFIED BY 'testdb'; GRANT ALL ON testdb.* TO testdb@'%'; FLUSH PRIVILEGES;"
add_delete_consumer test-resources/consumer-test.yaml mariadbconsumer-testing-2
echo -e "${YELLOW}====>${NOCOLOR} Seeded consumer logs"
check_operator_log | grep mariadbconsumer-testing-testing-2

echo -e "${GREEN}====>${NOCOLOR} Test seeded consumer V2"
echo "Test adding a seeded consumer with a specific environment type."
echo "This test already has pre-seeded database username and password, but will create the associated services"
docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb1; CREATE USER IF NOT EXISTS testdb1@'%' IDENTIFIED BY 'testdb1'; GRANT ALL ON testdb1.* TO testdb1@'%'; FLUSH PRIVILEGES;"
add_delete_consumer test-resources/consumer-test-2.yaml mariadbconsumer-testing-3
echo -e "${YELLOW}====>${NOCOLOR} Seeded consumer 2 logs"
check_operator_log | grep mariadbconsumer-testing-testing-3

echo -e "${GREEN}==>${NOCOLOR} Add an azure provider"
kubectl apply -f test-resources/provider-azure.yaml

echo -e "${GREEN}====>${NOCOLOR} Test blank azure consumer"
echo "Test adding a blank consumer with a specific environment type, but for azure"
echo "This test should create the database and user, and the associated services randomly"
echo "As this is for azure, the username should be `username@azurehostname`"
add_delete_consumer test-resources/consumer-azure.yaml mariadbconsumer-testing-azure
echo -e "${YELLOW}====>${NOCOLOR} Azure consumer logs"
check_operator_log | grep mariadbconsumer-testing-azure

echo -e "${GREEN}==>${NOCOLOR} Add multi providers"
kubectl apply -f test-resources/provider-multi.yaml
# testing multiple providers allows testing of the logic to ensure that the correct provider is chosen.

echo -e "${GREEN}====>${NOCOLOR} Test multi providers"
echo "Test adding a blank consumer with a specific environment type, but of a type that has multiple providers available"
echo "This test should create the database and user, and the associated services randomly, but choose the lowest table/schema count provider"
echo -e "${GREEN}======>${NOCOLOR} Create db multidb"
docker-compose exec -T mysql mysql --host=local-dbaas-provider-multi --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS multidb;CREATE TABLE multidb.Persons (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons2 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons3 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons4 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));"

echo -e "${GREEN}====>${NOCOLOR} Test blank multi consumer"
add_delete_consumer test-resources/consumer-multi.yaml mariadbconsumer-testing-multi
echo -e "${YELLOW}====>${NOCOLOR} Multi consumer logs"
check_operator_log | grep mariadbconsumer-testing-multi

echo -e "${GREEN}====>${NOCOLOR} Test multi providers part 2"
echo "Test adding a blank consumer with a specific environment type, but of a type that has multiple providers available"
echo "This test should create the database and user, and the associated services randomly, but choose the lowest table/schema count provider"
echo "This test adds additional tables to the first provider, so it should choose the second provider"
echo -e "${GREEN}======>${NOCOLOR} Create db multidb"
docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS multidb;CREATE TABLE multidb.Persons (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons2 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons3 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons4 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));"
echo -e "${GREEN}======>${NOCOLOR} Create db multidb2"
docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS multidb2;CREATE TABLE multidb2.Persons (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb2.Persons2 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb2.Persons3 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb2.Persons4 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));"

echo -e "${GREEN}====>${NOCOLOR} Test blank multi consumer part 2"
add_delete_consumer test-resources/consumer-multi2.yaml mariadbconsumer-testing-multi2 local-dbaas-provider-multi
echo -e "${YELLOW}====>${NOCOLOR} Multi consumer 2 logs"
check_operator_log | grep mariadbconsumer-testing-multi2
echo ""; echo ""
check_operator_log
tear_down
echo -e "${GREEN}================ END ================${NOCOLOR}"