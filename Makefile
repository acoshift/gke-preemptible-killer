build:
	docker build -t acoshift/gke-preemptible-killer .

publish: build
	docker push acoshift/gke-preemptible-killer
