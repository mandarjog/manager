// Copyright 2017 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Basic template engine using go templates

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ghodss/yaml"
	"github.com/golang/sync/errgroup"
	flag "github.com/spf13/pflag"

	"istio.io/manager/model"
	"istio.io/manager/platform/kube"
)

const (
	managerDiscovery     = "manager-discovery"
	mixer                = "mixer"
	egressProxy          = "egress-proxy"
	app                  = "app"
	appProxyManagerAgent = "app-proxy-manager-agent"
	// budget is the maximum number of retries with 1s delays
	budget = 30
)

var (
	kubeconfig      string
	inClusterConfig bool
	hub             string
	tag             string
	namespace       string
	verbose         bool
	norouting       bool
	parallel        bool

	client      *kubernetes.Clientset
	istioClient *kube.Client

	// pods is a mapping from app name to a pod name (write once, read only)
	pods map[string]string

	// accessLogs is a mapping from app name to a list of request ids that should be present in it
	accessLogs map[string][]string

	// mu protects mutable global state
	mu sync.Mutex
)

func init() {
	flag.StringVarP(&kubeconfig, "config", "c", "platform/kube/config",
		"kube config file (ignored if --in_cluster_config is set")
	flag.BoolVar(&inClusterConfig, "in_cluster_config", false,
		"Use in-cluster kube config")
	flag.StringVarP(&hub, "hub", "h", "gcr.io/istio-testing",
		"Docker hub")
	flag.StringVarP(&tag, "tag", "t", "",
		"Docker tag")
	flag.StringVarP(&namespace, "namespace", "n", "",
		"Namespace to use for testing (empty to create/delete temporary one)")
	flag.BoolVarP(&verbose, "dump", "d", false,
		"Dump proxy logs and request logs")
	flag.BoolVar(&norouting, "norouting", false,
		"Disable route rule tests")
	flag.BoolVar(&parallel, "parallel", true,
		"Run requests in parallel")
}

func main() {
	flag.Parse()
	setup()
	check(testBasicReachability())
	check(testRouting())
	teardown()
}

func setup() {
	if tag == "" {
		log.Fatal("No docker tag specified with -t or --tag")
	}
	log.Printf("hub %v, tag %v", hub, tag)

	check(setupClient())

	if namespace == "" {
		var err error
		if namespace, err = generateNamespace(client); err != nil {
			check(err)
		}
	}

	pods = make(map[string]string)

	// deploy istio-infra
	check(deploy("http-discovery", "http-discovery", managerDiscovery, namespace,
		"8080", "80", "unversioned"))
	check(deploy("mixer", "mixer", mixer, namespace, "8080", "80", "unversioned"))
	check(deploy("istio-egress", "istio-egress", egressProxy, namespace, "8080", "80", "unversioned"))

	//deploy a healthy mix of apps, with and without proxy
	check(deploy("t", "t", app, namespace, "8080", "80", "unversioned"))
	check(deploy("a", "a", appProxyManagerAgent, namespace, "8080", "80", "unversioned"))
	check(deploy("b", "b", appProxyManagerAgent, namespace, "80", "8080", "unversioned"))
	check(deploy("hello", "hello", appProxyManagerAgent, namespace, "8080", "80", "v1"))
	check(deploy("world-v1", "world", appProxyManagerAgent, namespace, "80", "8000", "v1"))
	check(deploy("world-v2", "world", appProxyManagerAgent, namespace, "80", "8000", "v2"))

	check(setPods())

	accessLogs = make(map[string][]string)
	for app := range pods {
		accessLogs[app] = make([]string, 0)
	}

}

// check function correctly cleans up on failure
func check(err error) {
	if err != nil {
		log.Print(err)
		teardown()
		os.Exit(1)
	}
}

// teardown removes resources
func teardown() {
	if verbose {
		dumpProxyLogs(pods["a"])
		dumpProxyLogs(pods["b"])
	}
	if namespace != "" && namespace != "default" {
		deleteNamespace(client, namespace)
		namespace = ""
	}
}

func deploy(name, svcName, dType, namespace, port1, port2, version string) error {
	// write template
	configFile := name + "-" + dType + ".yaml"
	var w *bufio.Writer
	f, err := os.Create(configFile)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("Error closing file " + configFile)
		}
	}()

	w = bufio.NewWriter(f)

	if err := write("test/integration/"+dType+".yaml.tmpl", map[string]string{
		"hub":       hub,
		"tag":       tag,
		"namespace": namespace,
		"service":   svcName,
		"name":      name,
		"port1":     port1,
		"port2":     port2,
		"version":   version,
	}, w); err != nil {
		return err
	}

	if err := w.Flush(); err != nil {
		return err
	}

	return run("kubectl apply -f " + configFile + " -n " + namespace)
}

func testBasicReachability() error {
	log.Printf("Verifying basic reachability across pods/services (a, b, and t)..")
	err := makeRequests()
	if err != nil {
		return err
	}
	if verbose {
		log.Println("requests:", accessLogs)
	}
	err = checkAccessLogs()
	if err != nil {
		return err
	}
	log.Println("Success!")
	return nil
}

func testRouting() error {
	if norouting {
		return nil
	}

	// First test default routing
	// Create a bytes buffer to hold the YAML form of rules
	log.Println("Routing all traffic to world-v1 and verifying..")
	var defaultRoute bytes.Buffer
	w := bufio.NewWriter(&defaultRoute)

	if err := write("test/integration/rule-default-route.yaml.tmpl", map[string]string{
		"destination": "world",
		"namespace":   namespace,
	}, w); err != nil {
		return err
	}

	if err := w.Flush(); err != nil {
		return err
	}

	epoch, err := getRestartEpoch("hello")
	if err != nil {
		return err
	}

	if err = addRule(defaultRoute.Bytes(), model.RouteRule, "default-route", namespace); err != nil {
		return err
	}

	if err = waitForNewRestartEpoch("hello", epoch); err != nil {
		return err
	}

	if err = verifyRouting("hello", "world", "", "",
		100, map[string]int{
			"v1": 100,
			"v2": 0,
		}); err != nil {
		return err
	}
	log.Println("Success!")

	log.Println("Routing 75 percent to world-v1, 25 percent to world-v2 and verifying..")
	// Create a bytes buffer to hold the YAML form of rules
	var weightedRoute bytes.Buffer
	w = bufio.NewWriter(&weightedRoute)

	if err = write("test/integration/rule-weighted-route.yaml.tmpl", map[string]string{
		"destination": "world",
		"namespace":   namespace,
	}, w); err != nil {
		return err
	}

	if err = w.Flush(); err != nil {
		return err
	}

	epoch, err = getRestartEpoch("hello")
	if err != nil {
		return err
	}

	if err = addRule(weightedRoute.Bytes(), model.RouteRule, "weighted-route", namespace); err != nil {
		return err
	}

	if err = waitForNewRestartEpoch("hello", epoch); err != nil {
		return err
	}

	if err = verifyRouting("hello", "world", "", "",
		100, map[string]int{
			"v1": 75,
			"v2": 25,
		}); err != nil {
		return err
	}
	log.Println("Success!")

	log.Println("Routing 100 percent to world-v2 using header based routing and verifying..")
	// Create a bytes buffer to hold the YAML form of rules
	var contentRoute bytes.Buffer
	w = bufio.NewWriter(&contentRoute)

	if err = write("test/integration/rule-content-route.yaml.tmpl", map[string]string{
		"destination": "world",
		"namespace":   namespace,
	}, w); err != nil {
		return err
	}

	if err = w.Flush(); err != nil {
		return err
	}

	epoch, err = getRestartEpoch("hello")
	if err != nil {
		return err
	}

	if err = addRule(contentRoute.Bytes(), model.RouteRule, "content-route", namespace); err != nil {
		return err
	}

	if err = waitForNewRestartEpoch("hello", epoch); err != nil {
		return err
	}

	if err = verifyRouting("hello", "world", "version", "v2",
		100, map[string]int{
			"v1": 0,
			"v2": 100,
		}); err != nil {
		return err
	}
	log.Println("Success!")

	log.Println("Testing fault injection..")
	// Create a bytes buffer to hold the YAML form of rules
	var faultPolicy bytes.Buffer
	w = bufio.NewWriter(&faultPolicy)

	if err = write("test/integration/policy-fault-injection.yaml.tmpl", map[string]string{
		"destination": "world",
		"namespace":   namespace,
	}, w); err != nil {
		return err
	}

	if err = w.Flush(); err != nil {
		return err
	}

	epoch, err = getRestartEpoch("hello")
	if err != nil {
		return err
	}

	if err = addRule(faultPolicy.Bytes(), model.Destination, "fault-policy", namespace); err != nil {
		return err
	}

	if err = waitForNewRestartEpoch("hello", epoch); err != nil {
		return err
	}

	if err = verifyFaultInjection(pods, "hello", "world", "version", "v2", time.Second*5, 503); err != nil {
		return err
	}
	log.Println("Success!")

	return nil
}

func waitForNewRestartEpoch(pod string, start int) error {
	log.Println("Waiting for Envoy restart epoch to increment...")
	for n := 0; n < budget; n++ {
		current, err := getRestartEpoch(pod)
		if err != nil {
			log.Printf("Could not obtain Envoy restart epoch for %s: %v", pod, err)
		}

		if current > start {
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("exceeded budget for waiting for envoy restart epoch to increment")
}

// getRestartEpoch gets the current restart epoch of a pod by calling the Envoy admin API.
func getRestartEpoch(pod string) (int, error) {
	url := "http://localhost:5000/server_info"
	cmd := fmt.Sprintf("kubectl exec %s -n %s -c app client %s", pods[pod], namespace, url)
	out, err := shell(cmd, true)
	if err != nil {
		return 0, err
	}

	// Response body is of the form: envoy 267724/RELEASE live 1571 1571 0
	// The last value is the restart epoch.
	match := regexp.MustCompile(`envoy .+/\w+ \w+ \d+ \d+ (\d+)`).FindStringSubmatch(out)
	if len(match) > 1 {
		epoch, err := strconv.ParseInt(match[1], 10, 32)
		return int(epoch), err
	}

	return 0, fmt.Errorf("could not obtain envoy restart epoch")
}

func addRule(ruleConfig []byte, kind string, name string, namespace string) error {

	out, err := yaml.YAMLToJSON(ruleConfig)
	if err != nil {
		return fmt.Errorf("Cannot convert YAML rule to JSON: %v", err)
	}

	istioKind, ok := model.IstioConfig[kind]
	if !ok {
		return fmt.Errorf("Invalid kind %s", kind)
	}
	v, err := istioKind.FromJSON(string(out))
	if err != nil {
		return fmt.Errorf("Cannot parse proto message from JSON: %v", err)
	}

	err = istioClient.Put(model.Key{
		Kind:      kind,
		Name:      name,
		Namespace: namespace,
	}, v)

	return err
}

func write(in string, data map[string]string, out io.Writer) error {
	tmpl, err := template.ParseFiles(in)
	if err != nil {
		return err
	}
	if err := tmpl.Execute(out, data); err != nil {
		return err
	}
	return nil
}

func run(command string) error {
	log.Println(command)
	parts := strings.Split(command, " ")
	/* #nosec */
	c := exec.Command(parts[0], parts[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func shell(command string, printCmd bool) (string, error) {
	if printCmd {
		log.Println(command)
	}
	parts := strings.Split(command, " ")
	/* #nosec */
	c := exec.Command(parts[0], parts[1:]...)
	bytes, err := c.CombinedOutput()
	if err != nil {
		log.Println(string(bytes))
		return "", fmt.Errorf("command failed: %q %v", string(bytes), err)
	}
	return string(bytes), nil
}

// connect to K8S cluster and register TPRs
func setupClient() error {
	var err error
	var config *rest.Config
	if inClusterConfig {
		config, err = rest.InClusterConfig()
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return err
	}

	client, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	istioClient, err = kube.NewClient(kubeconfig, model.IstioConfig)
	if err != nil {
		return err
	}

	return istioClient.RegisterResources()
}

func setPods() error {
	items := make([]v1.Pod, 0)
	for n := 0; ; n++ {
		log.Println("Checking all pods are running...")
		list, err := client.Pods(namespace).List(v1.ListOptions{})
		if err != nil {
			return err
		}
		items = list.Items
		ready := true

		for _, pod := range items {
			if pod.Status.Phase != "Running" {
				log.Printf("Pod %s has status %s\n", pod.Name, pod.Status.Phase)
				ready = false
				break
			}
		}

		if ready {
			break
		}

		if n > budget {
			for _, pod := range items {
				dumpProxyLogs(pod.Name)
			}
			return fmt.Errorf("exceeded budget for checking pod status")
		}

		time.Sleep(time.Second)
	}

	for _, pod := range items {
		if app, exists := pod.Labels["app"]; exists {
			pods[app] = pod.Name
		}
	}

	return nil
}

func dumpProxyLogs(name string) {
	log.Println("Pod proxy logs", name)
	raw, err := client.Pods(namespace).
		GetLogs(name, &v1.PodLogOptions{Container: "proxy"}).
		Do().Raw()
	if err != nil {
		log.Println("Request error", err)
	} else {
		log.Println(string(raw))
	}
}

// makeRequest creates a function to make requests; done should return true to quickly exit the retry loop
func makeRequest(src, dst, port, domain string, done func() bool) func() error {
	return func() error {
		url := fmt.Sprintf("http://%s%s%s/%s", dst, domain, port, src)
		for n := 0; n < budget; n++ {
			log.Printf("Making a request %s from %s (attempt %d)...\n", url, src, n)

			request, err := shell(fmt.Sprintf("kubectl exec %s -n %s -c app client %s", pods[src], namespace, url), verbose)
			if err != nil {
				return err
			}
			if verbose {
				log.Println(request)
			}
			match := regexp.MustCompile("X-Request-Id=(.*)").FindStringSubmatch(request)
			if len(match) > 1 {
				id := match[1]
				if verbose {
					log.Printf("id=%s\n", id)
				}
				mu.Lock()
				accessLogs[src] = append(accessLogs[src], id)
				accessLogs[dst] = append(accessLogs[dst], id)
				mu.Unlock()
				return nil
			}

			// Expected no match
			if src == "t" && dst == "t" {
				if verbose {
					log.Println("Expected no match for t->t")
				}
				return nil
			}
			if done() {
				return nil
			}
		}
		return fmt.Errorf("failed to inject proxy from %s to %s (url %s)", src, dst, url)
	}
}

// makeRequests executes requests in pods and collects request ids per pod to check against access logs
func makeRequests() error {
	log.Printf("makeRequests parallel=%t\n", parallel)
	g, ctx := errgroup.WithContext(context.Background())
	testPods := []string{"a", "b", "t"}
	for _, src := range testPods {
		for _, dst := range testPods {
			for _, port := range []string{"", ":80", ":8080"} {
				for _, domain := range []string{"", "." + namespace} {
					if parallel {
						g.Go(makeRequest(src, dst, port, domain, func() bool {
							select {
							case <-time.After(time.Second):
								// try again
							case <-ctx.Done():
								return true
							}
							return false
						}))
					} else {
						if err := makeRequest(src, dst, port, domain, func() bool { return false })(); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if parallel {
		if err := g.Wait(); err != nil {
			return err
		}
	}
	return nil
}

func checkAccessLogs() error {
	log.Println("Checking access logs of pods to correlate request IDs...")
	for n := 0; ; n++ {
		found := true
		for _, pod := range []string{"a", "b"} {
			if verbose {
				log.Printf("Checking access log of %s\n", pod)
			}
			access, err := shell(fmt.Sprintf("kubectl logs %s -n %s -c proxy", pods[pod], namespace), false)
			if err != nil {
				return err
			}
			for _, id := range accessLogs[pod] {
				if !strings.Contains(access, id) {
					if verbose {
						log.Printf("Failed to find request id %s in log of %s\n", id, pod)
					}
					found = false
					break
				}
			}
			if !found {
				break
			}
		}

		if found {
			return nil
		}

		if n > budget {
			return fmt.Errorf("exceeded budget for checking access logs")
		}

		time.Sleep(time.Second)
	}
}

// verifyRouting verifies if the traffic is split as specified across different deployments in a service
func verifyRouting(src, dst, headerKey, headerVal string,
	samples int, expectedCount map[string]int) error {
	var mu sync.Mutex
	count := make(map[string]int)
	for version := range expectedCount {
		count[version] = 0
	}

	url := fmt.Sprintf("http://%s/%s", dst, src)
	log.Printf("Making %d requests (%s) from %s...\n", samples, url, src)

	var g errgroup.Group
	for i := 0; i < samples; i++ {
		cmd := fmt.Sprintf("kubectl exec %s -n %s -c app client %s %s %s", pods[src], namespace, url, headerKey, headerVal)
		g.Go(func() error {
			request, err := shell(cmd, false)
			if err != nil {
				return err
			}
			if verbose {
				log.Println(request)
			}
			match := regexp.MustCompile("ServiceVersion=(.*)").FindStringSubmatch(request)
			if len(match) > 1 {
				id := match[1]
				mu.Lock()
				count[id]++
				mu.Unlock()
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return err
	}

	epsilon := 5

	var failures int
	for version, expected := range expectedCount {
		if count[version] > expected+epsilon || count[version] < expected-epsilon {
			log.Printf("Expected %v requests (+/-%v) to reach %s => Got %v\n", expected, epsilon, version, count[version])
			failures++
		}
	}

	if failures > 0 {
		return errors.New("routing verification failed")
	}
	return nil
}

// verifyFaultInjection verifies if the fault filter was setup properly
func verifyFaultInjection(pods map[string]string, src, dst, headerKey, headerVal string,
	respTime time.Duration, respCode int) error {

	url := fmt.Sprintf("http://%s/%s", dst, src)
	log.Printf("Making 1 request (%s) from %s...\n", url, src)
	cmd := fmt.Sprintf("kubectl exec %s -n %s -c app client %s %s %s", pods[src], namespace, url, headerKey, headerVal)

	start := time.Now()
	request, err := shell(cmd, false)
	elapsed := time.Since(start)
	if err != nil {
		return err
	}
	if verbose {
		log.Println(request)
	}

	match := regexp.MustCompile("StatusCode=(.*)").FindStringSubmatch(request)
	statusCode := 0
	if len(match) > 1 {
		statusCode, err = strconv.Atoi(match[1])
		if err != nil {
			statusCode = -1
		}
	}

	// +/- 1s variance
	epsilon := time.Second * 2
	log.Printf("Response time is %s with status code %d\n", elapsed, statusCode)
	log.Printf("Expected response time is %s +/- %s with status code %d\n", respTime, epsilon, respCode)
	if elapsed > respTime+epsilon || elapsed < respTime-epsilon || respCode != statusCode {
		return errors.New("fault injection verification failed")
	}
	return nil
}

func generateNamespace(cl *kubernetes.Clientset) (string, error) {
	ns, err := cl.Core().Namespaces().Create(&v1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "istio-integration-",
		},
	})
	if err != nil {
		return "", err
	}
	log.Printf("Created namespace %s\n", ns.Name)
	return ns.Name, nil
}

func deleteNamespace(cl *kubernetes.Clientset, ns string) {
	if cl != nil && ns != "" && ns != "default" {
		if err := cl.Core().Namespaces().Delete(ns, &v1.DeleteOptions{}); err != nil {
			log.Printf("Error deleting namespace: %v\n", err)
		}
		log.Printf("Deleted namespace %s\n", ns)
	}
}
