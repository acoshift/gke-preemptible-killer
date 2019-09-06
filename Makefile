COMMIT_SHA=$(shell git rev-parse HEAD)

build:
	docker build -t acoshift/gke-preemptible-killer .

publish: build
	docker tag acoshift/gke-preemptible-killer acoshift/gke-preemptible-killer:$(COMMIT_SHA)
	docker push acoshift/gke-preemptible-killer:$(COMMIT_SHA)
	docker push acoshift/gke-preemptible-killer
