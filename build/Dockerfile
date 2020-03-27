FROM golang:1.13
ENV GO111MODULE off
WORKDIR /go/src/github.com/AliyunContainerService/ack-kms-plugin
COPY . .
RUN make build

FROM alpine:3.11
WORKDIR /bin

ENV OPERATOR=/usr/local/bin/ack-secret-manager \
    USER_UID=1001 \
    USER_NAME=ack-secret-manager

COPY --from=0 /go/src/github.com/AliyunContainerService/ack-kms-plugin/ack-kms-plugin /bin/ack-kms-plugin

CMD ["/bin/ack-kms-plugin"]


FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

ENV OPERATOR=/usr/local/bin/ack-secret-manager \
    USER_UID=1001 \
    USER_NAME=ack-secret-manager

# install operator binary
COPY build/_output/bin/ack-secret-manager ${OPERATOR}

COPY build/bin /usr/local/bin
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
