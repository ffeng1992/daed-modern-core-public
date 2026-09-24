FROM node:22-bookworm-slim AS build-web

WORKDIR /build

COPY . .

RUN corepack enable
RUN corepack prepare pnpm@10.24.0 --activate
RUN pnpm install --frozen-lockfile
RUN pnpm build --filter daed


FROM golang:1.26.5-bookworm AS build-bundle

RUN \
    apt-get update; apt-get install -y --no-install-recommends git make llvm-15 clang-15 ca-certificates; \
    apt-get clean autoclean && apt-get autoremove -y && rm -rf /var/lib/{apt,dpkg,cache,log}/

# build bundle process
ENV CGO_ENABLED=0
ENV CLANG=clang-15
ARG DAED_VERSION=0.0.0-dev

COPY . /build/source
COPY --from=build-web /build/apps/web/dist /build/web

WORKDIR /build/source

RUN echo 'ae68a6cce28e2a2a3c80694e2c4f539c6cdf15ce15dde7560a66661dc9054481  experiments/ifindex-shutdown-join.patch' | sha256sum -c - && \
    echo '1115d45980cd2f01317ffb259b28fd43f1778a2e4f7922c07fc63fc769835ab6  experiments/sniffer-lifetime.patch' | sha256sum -c - && \
    cd wing/dae-core && \
    git apply --check ../../experiments/ifindex-shutdown-join.patch ../../experiments/sniffer-lifetime.patch && \
    git apply ../../experiments/ifindex-shutdown-join.patch ../../experiments/sniffer-lifetime.patch && \
    cd /build/source && \
    mkdir -p wing/webrender/web && cp -a /build/web/. wing/webrender/web/ && \
    make -C wing deps && \
    cd wing && CGO_ENABLED=0 go build -mod=readonly -trimpath -tags 'deployment_candidate,embedallowed' \
      -ldflags "-s -w -X github.com/daeuniverse/dae-wing/db.AppName=daed -X github.com/daeuniverse/dae-wing/db.AppVersion=$DAED_VERSION" \
      -o /build/daed .




FROM alpine:3.22

LABEL org.opencontainers.image.source=https://github.com/ffeng1992/daed-modern-core-public \
      org.opencontainers.image.description="Experimental daed v1.27.0 and DAE v2.1.1 integration bridge"

RUN mkdir -p /usr/local/share/daed/
RUN mkdir -p /etc/daed/
RUN apk add --no-cache ca-certificates
RUN wget -O /usr/local/share/daed/geoip.dat https://github.com/v2rayA/dist-v2ray-rules-dat/raw/master/geoip.dat && \
    wget -O /usr/local/share/daed/geosite.dat https://github.com/v2rayA/dist-v2ray-rules-dat/raw/master/geosite.dat
COPY --from=build-bundle /build/daed /usr/local/bin/daed

EXPOSE 2023
ENV DAED_ENABLE_VALIDATED_BRIDGE=1

CMD ["daed", "run", "-c", "/etc/daed"]
