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
