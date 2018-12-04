FROM alpine:3.8

RUN apk upgrade --update --no-cache

USER nobody

ADD build/_output/bin/worker-operator /usr/local/bin/worker-operator
