#FROM scratch
#
#LABEL maintainer="estafette.io" \
#      description="The estafette-gke-preemptible-killer component is a Kubernetes controller that ensures preemptible nodes in a Container Engine cluster don't expire at the same time"
#
#COPY ca-certificates.crt /etc/ssl/certs/
#COPY estafette-gke-preemptible-killer /
#
#CMD ["./estafette-gke-preemptible-killer"]

FROM golang:1.12.7-alpine as build

ENV GOPROXY=https://proxy.golang.org
ENV GO111MODULE=on

WORKDIR /workspace

ADD go.mod go.sum ./

RUN go mod download

ADD . .

RUN go build -o gke-preemptible-killer

FROM alpine

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=build /workspace/gke-preemptible-killer ./

ENTRYPOINT ["/app/gke-preemptible-killer"]
