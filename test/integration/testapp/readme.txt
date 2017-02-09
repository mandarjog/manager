This is a simple bookinfo application broken into four separate microservices:

productpage. The productpage microservice calls the details and reviews microservices to populate the page.

details. The details microservice contains book information.

reviews. The reviews microservice contains book reviews. It also calls the ratings microservice.

ratings. The ratings microservice contains book ranking information that accompanies a book review.

There are 3 versions of the reviews microservice:

Version v1 doesn’t call the ratings service.
Version v2 calls the ratings service, and displays each rating as 1 to 5 black stars.
Version v3 calls the ratings service, and displays each rating as 1 to 5 red stars.

To compile the apps, do
make build.productpage build.ratings build.reviews build.details

To create docker containers
make dockerize.productpage ... and so on. see Makefile for more targets.


Push these images to your dockerhub and change bookinfo.yaml to point to your dockerhub images accordingly.

For e.g., replace the docker.io/<NAMESPACE>/productpage-v1 with the appropriate image name for productpage-v1
such as docker.io/rshriram/bookinfo-productpage-v1

Once the bookinfo.yaml file is setup, you can launch the application in a minikube cluster with a simple

kubectl create -f bookinfo.yaml

You can query the productpage by ssh-ing into minikube, and querying the productpage service via usual kubernetes methods..
