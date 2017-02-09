launch manager and helloworld
kubectl create -f manager.yaml
kubectl create -f helloworld.yaml

Then exec into the gateway pod's app container.
kubectl exec -it gateway-pod-id -c app bash

You can curl the helloworld service using curl
http://helloworld.default.svc.local:9080/hello

You should see helloworld output from one of the two versions deployed
behind the helloworld service. For e.g.,

root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version2, container: helloworld-v2-3915138836-d5r1w
root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version1, container: helloworld-v1-3574023954-pppqr
root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version2, container: helloworld-v2-3915138836-d5r1w
root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version2, container: helloworld-v2-3915138836-d5r1w
root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version1, container: helloworld-v1-3574023954-pppqr
root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version2, container: helloworld-v2-3915138836-d5r1w

--
Need to create a routing rule using the manager cli and test weighted routing..

## TODO, this ends up in a seg fault. Do not try
To set upstreams
cat helloworld-default-upstream.yaml | ../../bazel-bin/cmd/manager/manager config put upstream-cluster helloworld-upstreams

To set default route,
cat helloworld-default-route-rules.yaml | ../../bazel-bin/cmd/manager/manager config put route-rule helloworld-default-route
To set 75/25 route
cat helloworld-v1-v2-route-rules.yaml | ../../bazel-bin/cmd/manager/manager config put route-rule helloworld-default-route

