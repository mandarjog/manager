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
