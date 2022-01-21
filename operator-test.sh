#!/bin/bash

NOCOLOR='\033[0m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
LIGHTBLUE='\033[1;34m'
MAGENTA='\033[1;35m'

POSTGRES_VERSION=$(cat test-resources/Dockerfile.postgres | grep FROM | awk '{print $2}')
MONGODB_VERSION=$(cat test-resources/Dockerfile.mongo | grep FROM | awk '{print $2}')

#KIND_VER=v1.13.12
#KIND_VER=v1.14.10
#KIND_VER=v1.15.7
#KIND_VER=v1.16.4
KIND_VER=v1.17.0
# or get the latest tagged version of a specific k8s version of kind
#KIND_VER=$(curl -s https://hub.docker.com/v2/repositories/kindest/node/tags | jq -r '.results | .[].name' | grep 'v1.17' | sort -Vr | head -1)
KIND_NAME=chart-testing
OPERATOR_IMAGE=amazeeio/dbaas-operator:test-tag
CHECK_TIMEOUT=10

check_operator_log () {
  echo -e "${GREEN}========= FULL OPERATOR LOG =========${NOCOLOR}"
  kubectl logs $(kubectl get pods  -n dbaas-operator-system --no-headers | awk '{print $1}') -c manager -n dbaas-operator-system
}

postgres_start_check () {
  until $(docker-compose exec -T local-dbaas-psql-provider psql -h localhost -p 5432 -U postgres postgres -c "SELECT datname FROM pg_database" | grep -q "postgres")
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

mariadb_start_check () {
  until $(docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e 'show databases;' | grep -q "information_schema")
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
  until $(docker-compose exec -T mysql mysql --host=local-dbaas-provider-mariadb-multi --port=3306 -uroot -e 'show databases;' | grep -q "information_schema")
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

mongodb_start_check () {
  until $(docker-compose exec -T local-dbaas-mongo-provider mongo mongodb://root:password@mongodb.172.17.0.1.nip.io:27017/  --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q admin)
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



mongodb_tls_start_check () {
  until $(docker-compose exec -T local-dbaas-mongo-tls-provider mongo --tls --tlsAllowInvalidCertificates --tlsAllowInvalidHostnames mongodb://root:password@mongodb.172.17.0.1.nip.io:27018/  --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q admin)
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
  echo -e "${GREEN}==> Ensure mariadb database providers are running${NOCOLOR}"
  mariadb_start_check
  CHECK_COUNTER=1
  echo -e "${LIGHTBLUE}==> Ensure postgres database provider is running${NOCOLOR}"
  postgres_start_check
  CHECK_COUNTER=1
  echo -e "${MAGENTA}==> Ensure mongodb database provider is running${NOCOLOR}"
  mongodb_start_check
  CHECK_COUNTER=1
  echo -e "${MAGENTA}==> Ensure mongodb tls database provider is running${NOCOLOR}"
  mongodb_tls_start_check
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
    tear_down
    exit 1
  fi
  done
}

check_services () {
  PRIMARY=$(kubectl get $2/$1 -o json | jq -r '.spec.consumer.services.primary')
  echo "====> Check primary service ${PRIMARY}"
  kubectl get service/${PRIMARY} -o yaml
}

check_services_replicas () {
  REPLICAS=$(kubectl get $2/$1 -o json | jq -r '.spec.consumer.services.replicas | .[]')
  for REPLICA in ${REPLICAS}
  do
  echo "====> Check replica service ${REPLICA}"
    kubectl get service/${REPLICA} -o yaml
  done
}

add_delete_consumer_mariadb_fail () {
  echo -e "${GREEN}====>${NOCOLOR} Add a consumer $1 $2"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until [ $(kubectl get mariadbconsumer/$2 -o json | jq -re '.metadata.annotations."dbaas.amazee.io/failed"?') == "true" ]
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Consumer not failed yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for consumer not failed reached."
    check_operator_log
    tear_down
    exit 1
  fi
  done
  echo -e "${GREEN}====>${NOCOLOR} Get MariaDBConsumer"
  kubectl get mariadbconsumer/$2 -o yaml
}

add_delete_consumer_mariadb () {
  echo -e "${GREEN}====>${NOCOLOR} Add a consumer $1 $2"
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
    tear_down
    exit 1
  fi
  done
  echo -e "${GREEN}====>${NOCOLOR} Get MariaDBConsumer"
  kubectl get mariadbconsumer/$2 -o yaml
  DB_NAME=$(kubectl get mariadbconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo -e "${GREEN}==>${NOCOLOR} Check if the operator creates the database"
  DB_EXISTS=$(docker-compose exec -T mysql mysql --host=${3:-local-dbaas-mariadb-provider} --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_NAME} exists"
  else 
    echo "database ${DB_NAME} does not exist"
    check_operator_log
    tear_down
    exit 1
  fi

  echo -e "${GREEN}==>${NOCOLOR} Check services"
  check_services $2 mariadbconsumer
  check_services_replicas $2 mariadbconsumer

  echo -e "${GREEN}==>${NOCOLOR} Delete the consumer"
  timeout 60 kubectl delete -f $1
  if [ $? -ne 0 ]
  then 
    echo "timed out waiting to delete consumer, check the logs to see why"
    check_operator_log
    tear_down
    exit 1
  fi
  echo -e "${GREEN}==>${NOCOLOR} Check if the operator deletes the database"
  DB_EXISTS=$(docker-compose exec -T mysql mysql --host=${3:-local-dbaas-mariadb-provider} --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_NAME} exists"
    check_operator_log
    tear_down
    exit 1
  else 
    echo "database ${DB_NAME} does not exist"
  fi
}

add_delete_consumer_psql () {
  echo -e "${GREEN}====>${NOCOLOR} Add a consumer"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until kubectl get postgresqlconsumer/$2 -o json | jq -e '.spec.consumer.database?'
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database not created yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database creation reached"
    check_operator_log
    tear_down
    exit 1
  fi
  done
  echo -e "${GREEN}====>${NOCOLOR} Get PostgreSQLConsumer"
  kubectl get postgresqlconsumer/$2 -o yaml
  DB_NAME=$(kubectl get postgresqlconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo -e "${GREEN}==>${NOCOLOR} Check if the operator creates the database"
  DB_EXISTS=$(docker-compose exec -T local-dbaas-psql-provider psql -h localhost -p 5432 -U postgres postgres --no-align --tuples-only -c "SELECT datname FROM pg_database;" | grep -q "${DB_NAME}")
  if [[ -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_NAME} exists"
  else 
    echo "database ${DB_NAME} does not exist"
    check_operator_log
    tear_down
    exit 1
  fi

  echo -e "${GREEN}==>${NOCOLOR} Check services"
  check_services $2 postgresqlconsumer

  echo -e "${GREEN}==>${NOCOLOR} Delete the consumer"
  timeout 60 kubectl delete -f $1
  if [ $? -ne 0 ]
  then
    echo "timed out waiting to delete consumer, check the logs to see why"
    check_operator_log
    tear_down
    exit 1
  fi
  echo -e "${GREEN}==>${NOCOLOR} Check if the operator deletes the database"
  DB_EXISTS=$(docker-compose exec -T local-dbaas-psql-provider psql -h localhost -p 5432 -U postgres postgres --no-align --tuples-only -c "SELECT datname FROM pg_database;" | grep -q "${DB_NAME}")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_NAME} exists"
    check_operator_log
    tear_down
    exit 1
  else 
    echo "database ${DB_NAME} does not exist"
  fi
}

add_delete_consumer_psql_fail () {
  echo -e "${GREEN}====>${NOCOLOR} Add a consumer $1 $2"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until [ $(kubectl get postgresqlconsumer/$2 -o json | jq -re '.metadata.annotations."dbaas.amazee.io/failed"?') == "true" ]
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Consumer not failed yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for consumer not failed reached."
    check_operator_log
    tear_down
    exit 1
  fi
  done
  echo -e "${GREEN}====>${NOCOLOR} Get PostgreSQLConsumer"
  kubectl get postgresqlconsumer/$2 -o yaml
}

add_delete_consumer_mongodb () {
  echo "====> Add a consumer"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until kubectl get mongodbconsumer/$2 -o json | jq -e '.spec.consumer.database?'
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database not created yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database creation reached"
    check_operator_log
    docker-compose logs local-dbaas-mongo-provider
    exit 1
  fi
  done
  echo "====> Get MongoDBConsumer"
  kubectl get mongodbconsumer/$2 -o yaml
  DB_NAME=$(kubectl get mongodbconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo "==> Check if the operator creates the database"
  if docker-compose exec -T local-dbaas-mongo-provider mongo mongodb://root:password@mongodb.172.17.0.1.nip.io:27017/  --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q "${DB_NAME}"
  then 
    echo "database ${DB_NAME} exists"
  else 
    echo "database ${DB_NAME} does not exist"
    check_operator_log
    exit 1
  fi

  echo "==> Check services"
  check_services $2 mongodbconsumer

  echo "==> Delete the consumer"
  timeout 60 kubectl delete -f $1
  if [ $? -ne 0 ]
  then
    echo "failed to delete consumer"
    check_operator_log
    exit 1
  fi
  echo "==> Check if the operator deletes the database"
  if docker-compose exec -T local-dbaas-mongo-provider mongo mongodb://root:password@mongodb.172.17.0.1.nip.io:27017/  --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q "${DB_NAME}"
  then 
    echo "database ${DB_NAME} exists"
    check_operator_log
    exit 1
  else 
    echo "database ${DB_NAME} does not exist"
  fi
}

add_delete_consumer_mongodb_fail () {
  echo -e "${GREEN}====>${NOCOLOR} Add a consumer $1 $2"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until [ $(kubectl get mongodbconsumer/$2 -o json | jq -re '.metadata.annotations."dbaas.amazee.io/failed"?') == "true" ]
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Consumer not failed yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for consumer not failed reached."
    check_operator_log
    tear_down
    exit 1
  fi
  done
  echo -e "${GREEN}====>${NOCOLOR} Get MongoDBConsumer"
  kubectl get mongodbconsumer/$2 -o yaml
}

add_delete_consumer_mongodb_tls () {
  echo "====> Add a consumer"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until kubectl get mongodbconsumer/$2 -o json | jq -e '.spec.consumer.database?'
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database not created yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database creation reached"
    check_operator_log
    docker-compose logs local-dbaas-mongo-tls-provider
    exit 1
  fi
  done
  echo "====> Get MongoDBConsumer"
  kubectl get mongodbconsumer/$2 -o yaml
  DB_NAME=$(kubectl get mongodbconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo "==> Check if the operator creates the database"
  if docker-compose exec -T local-dbaas-mongo-tls-provider mongo --tls --tlsAllowInvalidCertificates --tlsAllowInvalidHostnames mongodb://root:password@mongodb.172.17.0.1.nip.io:27018/ --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q "${DB_NAME}"
  then 
    echo "database ${DB_NAME} exists"
  else 
    echo "database ${DB_NAME} does not exist"
    check_operator_log
    exit 1
  fi

  echo "==> Check services"
  check_services $2 mongodbconsumer

  echo "==> Delete the consumer"
  timeout 60 kubectl delete -f $1
  if [ $? -ne 0 ]
  then
    echo "failed to delete consumer"
    check_operator_log
    exit 1
  fi
  echo "==> Check if the operator deletes the database"
  if docker-compose exec -T local-dbaas-mongo-tls-provider mongo --tls --tlsAllowInvalidCertificates --tlsAllowInvalidHostnames mongodb://root:password@mongodb.172.17.0.1.nip.io:27018/ --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q "${DB_NAME}"
  then 
    echo "database ${DB_NAME} exists"
    check_operator_log
    exit 1
  else 
    echo "database ${DB_NAME} does not exist"
  fi
}

add_delete_consumer_failure () {
  echo -e "${GREEN}====>${NOCOLOR} Add a consumer"
  kubectl apply -f $1
  echo -e "${GREEN}==>${NOCOLOR} Wait for consumer to fail"
  sleep 5
  echo -e "${GREEN}==>${NOCOLOR} Delete the consumer"
  kubectl delete -f $1
}

start_up
build_deploy_operator

echo -e "${GREEN}==>${GREEN}BusyBox: ${NOCOLOR} Add a busybox pod"
kubectl apply -f test-resources/busybox.yaml

echo -e "${GREEN}==>${YELLOW}MariaDB: ${NOCOLOR} Add a provider"
kubectl apply -f test-resources/mariadb/provider.yaml

echo -e "${GREEN}==>${YELLOW}MariaDB: ${NOCOLOR} Check provider"
kubectl exec -it busybox -- wget -q -O - http://backend.dbaas-operator-system.svc/mariadb/test

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test blank consumer"
echo "Test adding a blank consumer with a specific environment type."
echo "This test should create the database and user, and the associated services randomly"
add_delete_consumer_mariadb test-resources/mariadb/consumer.yaml mariadbconsumer-testing
echo -e "${YELLOW}====>${YELLOW}MariaDB: ${NOCOLOR} Blank consumer logs"
check_operator_log | grep mariadbconsumer-testing

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test seeded consumer"
echo "Test adding a seeded consumer with a specific environment type."
echo "This test already has pre-seeded database username and password, but will create the associated services"
docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb; CREATE USER IF NOT EXISTS testdb@'%' IDENTIFIED BY 'testdb'; GRANT ALL ON testdb.* TO testdb@'%'; FLUSH PRIVILEGES;"
add_delete_consumer_mariadb test-resources/mariadb/consumer-test.yaml mariadbconsumer-testing-2
echo -e "${YELLOW}====>${YELLOW}MariaDB: ${NOCOLOR} Seeded consumer logs"
check_operator_log | grep mariadbconsumer-testing-2

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test seeded consumer V2"
echo "Test adding a seeded consumer with a specific environment type."
echo "This test already has pre-seeded database username and password, but will create the associated services"
docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb1; CREATE USER IF NOT EXISTS testdb1@'%' IDENTIFIED BY 'testdb1'; GRANT ALL ON testdb1.* TO testdb1@'%'; FLUSH PRIVILEGES;"
add_delete_consumer_mariadb test-resources/mariadb/consumer-test-2.yaml mariadbconsumer-testing-3
echo -e "${YELLOW}====>${YELLOW}MariaDB: ${NOCOLOR} Seeded consumer 2 logs"
check_operator_log | grep mariadbconsumer-testing-3

echo "Test adding a blank consumer with a non existing environment type."
echo "This test should fail and set the failure annotations"
add_delete_consumer_mariadb_fail test-resources/mariadb/consumer-fail-1.yaml mariadbconsumer-testing-fail1
echo -e "${YELLOW}====>${YELLOW}MariaDB: ${NOCOLOR} Failed consumer logs"
check_operator_log | grep mariadbconsumer-testing-fail1

echo -e "${GREEN}==>${YELLOW}MariaDB: ${NOCOLOR} Add an azure provider"
kubectl apply -f test-resources/mariadb/provider-azure.yaml

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test blank azure consumer"
echo "Test adding a blank consumer with a specific environment type, but for azure"
echo "This test should create the database and user, and the associated services randomly"
echo "As this is for azure, the username should be 'username@azurehostname'"
add_delete_consumer_mariadb test-resources/mariadb/consumer-azure.yaml mariadbconsumer-testing-azure
echo -e "${YELLOW}====>${YELLOW}MariaDB: ${NOCOLOR} Azure consumer logs"
check_operator_log | grep mariadbconsumer-testing-azure

echo -e "${GREEN}==>${YELLOW}MariaDB: ${NOCOLOR} Add multi providers"
kubectl apply -f test-resources/mariadb/provider-multi.yaml
# testing multiple providers allows testing of the logic to ensure that the correct provider is chosen.

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test multi providers"
echo "Test adding a blank consumer with a specific environment type, but of a type that has multiple providers available"
echo "This test should create the database and user, and the associated services randomly, but choose the lowest table/schema count provider"
echo -e "${GREEN}======>${YELLOW}MariaDB: ${NOCOLOR} Create db multidb"
docker-compose exec -T mysql mysql --host=local-dbaas-provider-mariadb-multi --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS multidb;CREATE TABLE multidb.Persons (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons2 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons3 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons4 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));"

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test blank multi consumer"
add_delete_consumer_mariadb test-resources/mariadb/consumer-multi.yaml mariadbconsumer-testing-multi
echo -e "${YELLOW}====>${YELLOW}MariaDB: ${NOCOLOR} Multi consumer logs"
check_operator_log | grep mariadbconsumer-testing-multi
docker-compose exec -T mysql mysql --host=local-dbaas-provider-mariadb-multi --port=3306 -uroot -e "DROP DATABASE multidb;"

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test multi providers part 2"
echo "Test adding a blank consumer with a specific environment type, but of a type that has multiple providers available"
echo "This test should create the database and user, and the associated services randomly, but choose the lowest table/schema count provider"
echo "This test adds additional tables to the first provider, so it should choose the second provider"
echo -e "${GREEN}======>${YELLOW}MariaDB: ${NOCOLOR} Create db multidb"
docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS multidb;CREATE TABLE multidb.Persons (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons2 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons3 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb.Persons4 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));"
echo -e "${GREEN}======>${YELLOW}MariaDB: ${NOCOLOR} Create db multidb2"
docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS multidb2;CREATE TABLE multidb2.Persons (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb2.Persons2 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb2.Persons3 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));CREATE TABLE multidb2.Persons4 (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));"

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test blank multi consumer part 2"
add_delete_consumer_mariadb test-resources/mariadb/consumer-multi2.yaml mariadbconsumer-testing-multi2 local-dbaas-provider-mariadb-multi
echo -e "${YELLOW}====>${YELLOW}MariaDB: ${NOCOLOR} Multi consumer 2 logs"
check_operator_log | grep mariadbconsumer-testing-multi2
docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e "DROP DATABASE multidb;"
docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e "DROP DATABASE multidb2;"

echo -e "${GREEN}====>${YELLOW}MariaDB: ${NOCOLOR} Test blank azure consumer with long hostname"
echo "Test adding a blank consumer with a specific environment type, but for azure"
echo "This test should attempt to create the database, but fail at the user creation"
echo "As this is for azure, the username should be 'username@azurehostname'"
echo "Testing for the failure, it should give up trying to create the user"
add_delete_consumer_failure test-resources/mariadb/consumer-azure-long.yaml mariadbconsumer-testing-azure-long
echo -e "${YELLOW}====>${YELLOW}MariaDB: ${NOCOLOR} Azure consumer logs"
check_operator_log | grep mariadbconsumer-testing-azure-long
DB_EXISTS=$(docker-compose exec -T mysql mysql --host=${3:-local-dbaas-mariadb-provider} --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata;" | egrep -v "information_schema|^db|performance_schema|mysql")
if [[ ! -z "${DB_EXISTS}" ]]
then
    echo "databases exist when they shouldn't"
    check_operator_log
    tear_down
    exit 1
fi

echo -e "${GREEN}==>${LIGHTBLUE}PostgreSQL: ${NOCOLOR} Test PostgreSQL"
echo -e "${GREEN}====>${LIGHTBLUE}PostgreSQL: ${NOCOLOR} Add a provider"
kubectl apply -f test-resources/postgres/provider.yaml
kubectl get postgresqlprovider/postgreprovider-testing -o yaml

echo -e "${GREEN}====>${LIGHTBLUE}PostgreSQL: ${NOCOLOR} Test blank consumer"
add_delete_consumer_psql test-resources/postgres/consumer.yaml psqlconsumer-testing
echo -e "${YELLOW}====>${LIGHTBLUE}PostgreSQL: ${NOCOLOR} Blank consumer logs"
check_operator_log | grep psqlconsumer-testing

echo "Test adding a blank consumer with a non existing environment type."
echo "This test should fail and set the failure annotations"
add_delete_consumer_psql_fail test-resources/postgres/consumer-fail-1.yaml psqlconsumer-testing-fail1
echo -e "${YELLOW}====>${YELLOW}PostgreSQL: ${NOCOLOR} Failed consumer logs"
check_operator_log | grep psqlconsumer-testing-fail1

echo -e "${GREEN}==>${MAGENTA}MongoDB: ${NOCOLOR} Test MongoDB"
echo -e "${GREEN}====>${MAGENTA}MongoDB: ${NOCOLOR} Add a provider"
kubectl apply -f test-resources/mongodb/provider.yaml
kubectl get mongodbprovider/mongodbprovider-testing -o yaml

echo -e "${GREEN}====>${MAGENTA}MongoDB: ${NOCOLOR} Test blank consumer"
add_delete_consumer_mongodb test-resources/mongodb/consumer.yaml mongodbconsumer-testing
echo -e "${YELLOW}====>${MAGENTA}MongoDB: ${NOCOLOR} Blank consumer logs"
check_operator_log | grep mongodbconsumer-testing

echo "Test adding a blank consumer with a non existing environment type."
echo "This test should fail and set the failure annotations"
add_delete_consumer_mongodb_fail test-resources/mongodb/consumer-fail-1.yaml mongodbconsumer-testing-fail1
echo -e "${YELLOW}====>${YELLOW}MongoDB: ${NOCOLOR} Failed consumer logs"
check_operator_log | grep mongodbconsumer-testing-fail1

echo -e "${GREEN}==>${MAGENTA}MongoDB: ${NOCOLOR} Test MongoDB TLS"
echo -e "${GREEN}====>${MAGENTA}MongoDB: ${NOCOLOR} Add a TLS provider"
kubectl apply -f test-resources/mongodb/provider-tls.yaml
kubectl get mongodbprovider/mongodbprovider-tls-testing -o yaml

echo -e "${GREEN}====>${MAGENTA}MongoDB: ${NOCOLOR} Test blank TLS consumer"
add_delete_consumer_mongodb_tls test-resources/mongodb/consumer-tls.yaml mongodbconsumer-tls-testing
echo -e "${YELLOW}====>${MAGENTA}MongoDB: ${NOCOLOR} Blank TLS consumer logs"
check_operator_log | grep mongodbconsumer-tls-testing

echo ""; echo ""
tear_down
echo -e "${GREEN}================ END ================${NOCOLOR}"