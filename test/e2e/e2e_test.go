package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/amazeeio/dbaas-operator/test/utils"
)

const (
	operatorImage     = "amazeeio/dbaas-operator:test-tag"
	operatorNamespace = "dbaas-operator-system"
	backendURL        = "http://dbaas-operator-controller-backend.dbaas-operator-system.svc:5000"
	mariadbHost       = "local-dbaas-mariadb-provider"
	mariadbMultiHost  = "local-dbaas-provider-mariadb-multi"
	mongoURI          = "mongodb://root:password@mongodb.172.17.0.1.nip.io:27017/"
	mongoTLSURI       = "mongodb://root:password@mongodb.172.17.0.1.nip.io:27018/"
	providerTimeout   = 3 * time.Minute
	reconcileTimeout  = 2 * time.Minute
	poll              = 3 * time.Second
)

func kubectl(args ...string) string {
	ginkgo.GinkgoHelper()
	output, err := utils.RunKubectl(args...)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return output
}

func apply(file string) {
	ginkgo.GinkgoHelper()
	kubectl("apply", "-f", file)
}

func deleteFile(file string) {
	ginkgo.GinkgoHelper()
	kubectl("delete", "-f", file, "--timeout=60s")
}

func jsonpath(resource, path string) string {
	output, err := utils.RunKubectl("get", resource, "-o", "jsonpath="+path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func dumpOperatorLogs() {
	output, _ := utils.RunKubectl("logs", "-n", operatorNamespace, "-l", "control-plane=controller-manager", "-c", "manager", "--tail=-1")
	_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "=== operator logs ===\n%s\n", output)
}

type providerResult struct {
	Result struct {
		Found bool `json:"found"`
	} `json:"result"`
	Error string `json:"error"`
}

func queryProvider(path string) providerResult {
	ginkgo.GinkgoHelper()
	output := kubectl("exec", "busybox", "--", "wget", "-q", "-O", "-", backendURL+path)
	var result providerResult
	gomega.Expect(json.Unmarshal([]byte(output), &result)).To(gomega.Succeed(), output)
	return result
}

func expectProviderFound(path string, found bool) {
	ginkgo.GinkgoHelper()
	gomega.Eventually(func() bool {
		output, err := utils.RunKubectl("exec", "busybox", "--", "wget", "-q", "-O", "-", backendURL+path)
		if err != nil {
			return !found
		}
		var result providerResult
		if json.Unmarshal([]byte(output), &result) != nil {
			return !found
		}
		return result.Result.Found
	}, reconcileTimeout, poll).Should(gomega.Equal(found))
}

func mysqlExec(host, query string) (string, error) {
	return utils.Compose("exec", "-T", "mysql", "mysql", "--host="+host, "--port=3306", "-uroot", "-qfsBNe", query)
}

func mariadbDatabases(host string) []string {
	ginkgo.GinkgoHelper()
	output, err := mysqlExec(host, "SHOW DATABASES;")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return utils.GetNonEmptyLines(output)
}

func postgresDatabases() []string {
	ginkgo.GinkgoHelper()
	output, err := utils.Compose("exec", "-T", "local-dbaas-psql-provider", "psql", "-h", "localhost", "-p", "5432",
		"-U", "postgres", "postgres", "--no-align", "--tuples-only", "-c", "SELECT datname FROM pg_database;")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return utils.GetNonEmptyLines(output)
}

func mongoDatabases(service, uri string, tls bool) []string {
	ginkgo.GinkgoHelper()
	args := []string{"exec", "-T", service, "mongo"}
	if tls {
		args = append(args, "--tls", "--tlsAllowInvalidCertificates", "--tlsAllowInvalidHostnames")
	}
	args = append(args, uri, "--quiet", "--eval", "db.getMongo().getDBNames().forEach(function(db){print(db)})")
	output, err := utils.Compose(args...)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return utils.GetNonEmptyLines(output)
}

func waitForDatabaseName(resource string) string {
	ginkgo.GinkgoHelper()
	var name string
	gomega.Eventually(func() string {
		name = jsonpath(resource, "{.spec.consumer.database}")
		return name
	}, reconcileTimeout, poll).ShouldNot(gomega.BeEmpty())
	return name
}

func expectConsumerFails(resource string) {
	ginkgo.GinkgoHelper()
	gomega.Eventually(func() string {
		return jsonpath(resource, `{.metadata.annotations.dbaas\.amazee\.io/failed}`)
	}, reconcileTimeout, poll).Should(gomega.Equal("true"))
}

func expectServices(resource string, replicas bool) {
	ginkgo.GinkgoHelper()
	primary := jsonpath(resource, "{.spec.consumer.services.primary}")
	gomega.Expect(primary).NotTo(gomega.BeEmpty())
	kubectl("get", "service/"+primary)
	if replicas {
		for _, replica := range strings.Fields(jsonpath(resource, "{.spec.consumer.services.replicas[*]}")) {
			kubectl("get", "service/"+replica)
		}
	}
}

func execMariadb(host, query string) {
	ginkgo.GinkgoHelper()
	_, err := utils.Compose("exec", "-T", "mysql", "mysql", "--host="+host, "--port=3306", "-uroot", "-e", query)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func personTables(db string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "CREATE DATABASE IF NOT EXISTS %s;", db)
	for _, table := range []string{"Persons", "Persons2", "Persons3", "Persons4"} {
		fmt.Fprintf(&sb, "CREATE TABLE %s.%s (PersonID int,LastName varchar(255),FirstName varchar(255),Address varchar(255),City varchar(255));", db, table)
	}
	return sb.String()
}

func mariadbConsumerLifecycle(file, name, host string) {
	ginkgo.GinkgoHelper()
	resource := "mariadbconsumer/" + name
	apply(file)
	dbName := waitForDatabaseName(resource)
	gomega.Expect(mariadbDatabases(host)).To(gomega.ContainElement(dbName))
	expectServices(resource, true)
	deleteFile(file)
	gomega.Expect(mariadbDatabases(host)).NotTo(gomega.ContainElement(dbName))
}

var _ = ginkgo.Describe("dbaas-operator", ginkgo.Ordered, func() {
	ginkgo.BeforeAll(func() {
		ginkgo.By("starting the local database providers")
		_, err := utils.Compose("up", "-d", "--build")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Eventually(func() error {
			_, err := mysqlExec(mariadbHost, "SHOW DATABASES;")
			return err
		}, providerTimeout, 5*time.Second).Should(gomega.Succeed())
		gomega.Eventually(func() error {
			_, err := mysqlExec(mariadbMultiHost, "SHOW DATABASES;")
			return err
		}, providerTimeout, 5*time.Second).Should(gomega.Succeed())
		gomega.Eventually(func() []string { return postgresDatabases() }, providerTimeout, 5*time.Second).Should(gomega.ContainElement("postgres"))
		gomega.Eventually(func() []string { return mongoDatabases("local-dbaas-mongo-provider", mongoURI, false) }, providerTimeout, 5*time.Second).Should(gomega.ContainElement("admin"))
		gomega.Eventually(func() []string { return mongoDatabases("local-dbaas-mongo-tls-provider", mongoTLSURI, true) }, providerTimeout, 5*time.Second).Should(gomega.ContainElement("admin"))

		ginkgo.By("building and deploying the operator")
		_, err = utils.Run("docker", "build", ".", "-t", operatorImage)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(utils.LoadImageToKindClusterWithName(operatorImage)).To(gomega.Succeed())
		_, err = utils.Run("make", "deploy", "IMG="+operatorImage)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		kubectl("rollout", "status", "deployment", "-n", operatorNamespace, "dbaas-operator-controller-manager", "--timeout=180s")
		apply("test-resources/busybox.yaml")
		kubectl("wait", "--for=condition=Ready", "pod/busybox", "--timeout=120s")
	})

	ginkgo.AfterAll(func() {
		_, _ = utils.RunKubectl("delete", "-f", "test-resources/busybox.yaml", "--ignore-not-found", "--wait=false")
		for _, kind := range []string{"mariadbconsumers", "postgresqlconsumers", "mongodbconsumers", "mariadbproviders", "postgresqlproviders", "mongodbproviders"} {
			_, _ = utils.RunKubectl("delete", kind, "--all", "--ignore-not-found", "--timeout=60s")
		}
		_, _ = utils.Run("make", "undeploy-operator")
		_, _ = utils.Compose("down")
	})

	ginkgo.AfterEach(func() {
		if ginkgo.CurrentSpecReport().Failed() {
			dumpOperatorLogs()
		}
	})

	ginkgo.Context("MariaDB", func() {
		ginkgo.It("reports no provider for an unknown environment", func() {
			expectProviderFound("/mariadb/non-existent", false)
		})

		ginkgo.It("finds a provider and manages blank and seeded consumers", func() {
			apply("test-resources/mariadb/provider.yaml")
			expectProviderFound("/mariadb/test", true)

			mariadbConsumerLifecycle("test-resources/mariadb/consumer.yaml", "mariadbconsumer-testing", mariadbHost)

			execMariadb(mariadbHost, "CREATE DATABASE IF NOT EXISTS testdb; CREATE USER IF NOT EXISTS testdb@'%' IDENTIFIED BY 'testdb'; GRANT ALL ON testdb.* TO testdb@'%'; FLUSH PRIVILEGES;")
			mariadbConsumerLifecycle("test-resources/mariadb/consumer-test.yaml", "mariadbconsumer-testing-2", mariadbHost)

			execMariadb(mariadbHost, "CREATE DATABASE IF NOT EXISTS testdb1; CREATE USER IF NOT EXISTS testdb1@'%' IDENTIFIED BY 'testdb1'; GRANT ALL ON testdb1.* TO testdb1@'%'; FLUSH PRIVILEGES;")
			mariadbConsumerLifecycle("test-resources/mariadb/consumer-test-2.yaml", "mariadbconsumer-testing-3", mariadbHost)
		})

		ginkgo.It("fails a consumer with a non existing environment type", func() {
			apply("test-resources/mariadb/consumer-fail-1.yaml")
			expectConsumerFails("mariadbconsumer/mariadbconsumer-testing-fail1")
			deleteFile("test-resources/mariadb/consumer-fail-1.yaml")
		})

		ginkgo.It("handles azure providers", func() {
			apply("test-resources/mariadb/provider-azure.yaml")
			expectProviderFound("/mariadb/azure", true)
			mariadbConsumerLifecycle("test-resources/mariadb/consumer-azure.yaml", "mariadbconsumer-testing-azure", mariadbHost)
		})

		ginkgo.It("chooses the provider with the lowest schema count", func() {
			apply("test-resources/mariadb/provider-multi.yaml")
			expectProviderFound("/mariadb/multi", true)

			execMariadb(mariadbMultiHost, personTables("multidb"))
			mariadbConsumerLifecycle("test-resources/mariadb/consumer-multi.yaml", "mariadbconsumer-testing-multi", mariadbHost)
			execMariadb(mariadbMultiHost, "DROP DATABASE multidb;")

			execMariadb(mariadbHost, personTables("multidb")+personTables("multidb2"))
			mariadbConsumerLifecycle("test-resources/mariadb/consumer-multi2.yaml", "mariadbconsumer-testing-multi2", mariadbMultiHost)
			execMariadb(mariadbHost, "DROP DATABASE multidb; DROP DATABASE multidb2;")
		})

		ginkgo.It("does not create databases for azure consumers with a long hostname", func() {
			file := "test-resources/mariadb/consumer-azure-long.yaml"
			apply(file)
			time.Sleep(10 * time.Second)
			deleteFile(file)
			for _, db := range mariadbDatabases(mariadbHost) {
				gomega.Expect(db).To(gomega.BeElementOf("information_schema", "db", "performance_schema", "mysql", "sys"))
			}
		})
	})

	ginkgo.Context("PostgreSQL", func() {
		ginkgo.It("finds a provider and manages a consumer", func() {
			apply("test-resources/postgres/provider.yaml")
			expectProviderFound("/postgres/test", true)

			file := "test-resources/postgres/consumer.yaml"
			resource := "postgresqlconsumer/psqlconsumer-testing"
			apply(file)
			dbName := waitForDatabaseName(resource)
			gomega.Expect(postgresDatabases()).To(gomega.ContainElement(dbName))
			expectServices(resource, false)
			deleteFile(file)
			gomega.Expect(postgresDatabases()).NotTo(gomega.ContainElement(dbName))
		})

		ginkgo.It("fails a consumer with a non existing environment type", func() {
			apply("test-resources/postgres/consumer-fail-1.yaml")
			expectConsumerFails("postgresqlconsumer/psqlconsumer-testing-fail1")
			deleteFile("test-resources/postgres/consumer-fail-1.yaml")
		})
	})

	ginkgo.Context("MongoDB", func() {
		lifecycle := func(file, name, service, uri string, tls bool) {
			resource := "mongodbconsumer/" + name
			apply(file)
			dbName := waitForDatabaseName(resource)
			gomega.Expect(mongoDatabases(service, uri, tls)).To(gomega.ContainElement(dbName))
			expectServices(resource, false)
			deleteFile(file)
			gomega.Expect(mongoDatabases(service, uri, tls)).NotTo(gomega.ContainElement(dbName))
		}

		ginkgo.It("finds a provider and manages a consumer", func() {
			apply("test-resources/mongodb/provider.yaml")
			expectProviderFound("/mongodb/test", true)
			lifecycle("test-resources/mongodb/consumer.yaml", "mongodbconsumer-testing", "local-dbaas-mongo-provider", mongoURI, false)
		})

		ginkgo.It("fails a consumer with a non existing environment type", func() {
			apply("test-resources/mongodb/consumer-fail-1.yaml")
			expectConsumerFails("mongodbconsumer/mongodbconsumer-testing-fail1")
			deleteFile("test-resources/mongodb/consumer-fail-1.yaml")
		})

		ginkgo.It("manages a consumer using a TLS provider", func() {
			apply("test-resources/mongodb/provider-tls.yaml")
			expectProviderFound("/mongodb/tls-test", true)
			lifecycle("test-resources/mongodb/consumer-tls.yaml", "mongodbconsumer-tls-testing", "local-dbaas-mongo-tls-provider", mongoTLSURI, true)
		})
	})
})
