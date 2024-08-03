# This Dockerfile requires DOCKER_BUILDKIT=1 to be build.
# We do not use syntax header so that we do not have to wait
# for the Dockerfile frontend image to be pulled.
FROM golang:1.22-alpine3.18 AS build

RUN apk --update add make bash git gcc musl-dev tzdata && \
  adduser -D -H -g "" -s /sbin/nologin -u 1000 user
COPY . /go/src/fun
WORKDIR /go/src/fun
# We want Docker image for build timestamp label to match the one in
# the binary so we take a timestamp once outside and pass it in.
ARG BUILD_TIMESTAMP
RUN \
  BUILD_TIMESTAMP=$BUILD_TIMESTAMP make build-static && \
  mv fun /go/bin/fun

FROM alpine:3.18 AS debug
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /etc/group /etc/group
COPY --from=build /go/bin/fun /
ENTRYPOINT ["/fun"]

FROM scratch AS production
RUN --mount=from=busybox:1.36-musl,src=/bin/,dst=/bin/ ["/bin/mkdir", "-m", "1755", "/tmp"]
COPY --from=build /etc/services /etc/services
COPY --from=build /etc/protocols /etc/protocols
# Apart from the USER statement, the rest is the same as for the debug image.
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /etc/group /etc/group
COPY --from=build /go/bin/fun /
USER user:user
ENTRYPOINT ["/fun"]
