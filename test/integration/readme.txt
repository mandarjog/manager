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
Need to create a routing rule using the manager cli to set default for
helloworld to v1

cat helloworld-default-route-rules.yaml | ../../bazel-bin/cmd/manager/manager config put route-rule helloworld-default-route

Then exec into the gateway pod's app container.
kubectl exec -it gateway-pod-id -c app bash

You can curl the helloworld service using curl
http://helloworld.default.svc.local:9080/hello

You should see helloworld output only from version 1

root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version1, container: helloworld-v1-3574023954-pppqr
root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version1, container: helloworld-v1-3574023954-pppqr
root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version1, container: helloworld-v1-3574023954-pppqr
root@gateway-350896508-mxdb4:/opt/microservices# curl
http://helloworld.default.svc:9080/hello
Hello version: version1, container: helloworld-v1-3574023954-pppqr

----

To set 75/25 route
cat helloworld-v1-v2-route-rules.yaml | ../../bazel-bin/cmd/manager/manager config put route-rule helloworld-v1-v2-route
And then delete the older default route (this is a bug, and will be fixed in next routing spec).
../../bazel-bin/cmd/manager/manager config delete route-rule helloworld-default-route

Now, try to access helloworld from the gateway container.. You might have to run curl 100 times to see the distribution.

exec into the gateway pod's app container.
kubectl exec -it gateway-pod-id -c app bash

Run curl 100 times
root@gateway-4087958066-rtdb5:/opt/microservices# for i in `seq 1 100`; do curl http://helloworld.default.svc.cluster.local:9080/hello >>a; done

Check the distribution of requests between v1 and v2
root@gateway-4087958066-rtdb5:/opt/microservices# cat a|sort|uniq -c
75 Hello version: version1, container: helloworld-v1-802377672-pn46c
25 Hello version: version2, container: helloworld-v2-1145851850-9mmkr
---
