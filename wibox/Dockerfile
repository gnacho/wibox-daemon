ARG BUILD_FROM
FROM golang:1.27-alpine AS build

# The Supervisor builds an add-on with its own folder as the build context, so
# the Go sources cannot be copied from the repository root. They come from the
# tag that matches the add-on version instead, which also pins what gets built.
ARG WIBOX_VERSION=0.2.1
ADD https://github.com/gnacho/wibox-daemon/archive/refs/tags/v${WIBOX_VERSION}.tar.gz /tmp/src.tar.gz
RUN mkdir -p /src && tar -xzf /tmp/src.tar.gz -C /src --strip-components=1
WORKDIR /src
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${WIBOX_VERSION}" -o /out/wibox-daemon .

FROM $BUILD_FROM

RUN apk add --no-cache ca-certificates jq

COPY --from=build /out/wibox-daemon /usr/bin/wibox-daemon
COPY run.sh /run.sh
RUN chmod a+x /run.sh

CMD [ "/run.sh" ]
