ARG BUILD_FROM
FROM golang:1.27-alpine AS build

# The Supervisor builds an add-on with its own folder as the build context, so
# the Go sources cannot be copied from the repository root. They come from the
# tag that matches the add-on version instead, which also pins what gets built.
ARG WIBOX_VERSION=0.5.1
ADD https://github.com/gnacho/wibox-daemon/archive/refs/tags/v${WIBOX_VERSION}.tar.gz /tmp/src.tar.gz
RUN mkdir -p /src && tar -xzf /tmp/src.tar.gz -C /src --strip-components=1
WORKDIR /src
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${WIBOX_VERSION}" -o /out/wibox-daemon .

FROM $BUILD_FROM

# go2rtc turns the daemon's H264 stdout into RTSP/WebRTC for Home Assistant.
# It has to live in the same container because its exec source can only spawn
# a binary that exists inside it.
ARG BUILD_ARCH=amd64
ARG GO2RTC_VERSION=1.9.15

RUN apk add --no-cache ca-certificates jq curl \
 && case "${BUILD_ARCH}" in aarch64) arch=arm64 ;; *) arch=amd64 ;; esac \
 && curl -fsSL -o /usr/bin/go2rtc \
      "https://github.com/AlexxIT/go2rtc/releases/download/v${GO2RTC_VERSION}/go2rtc_linux_${arch}" \
 && chmod +x /usr/bin/go2rtc \
 && apk del curl

COPY --from=build /out/wibox-daemon /usr/bin/wibox-daemon
COPY run.sh /run.sh
RUN chmod a+x /run.sh

CMD [ "/run.sh" ]
