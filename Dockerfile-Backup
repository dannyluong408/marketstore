FROM golang:1.10.3-alpine

ARG tag

ENV DOCKER_TAG=$tag

RUN apk update
RUN apk --no-cache add git make tar bash curl alpine-sdk su-exec
#RUN  go get -u github.com/golang/dep/... && mv /go/bin/dep /usr/local/bin/dep
RUN curl https://raw.githubusercontent.com/golang/dep/master/install.sh | sh && mv /go/bin/dep /usr/local/bin/dep
ADD . /go/src/github.com/dannyluong408/marketstore
WORKDIR /go/src/github.com/dannyluong408/marketstore

RUN make configure all plugins

COPY entrypoint.sh /bin/
RUN chmod +x /bin/entrypoint.sh
ENTRYPOINT ["/bin/entrypoint.sh"]

CMD marketstore
