ARG BUILDER_IMAGE
FROM ${BUILDER_IMAGE}

# This stage only compiles acceptance artifacts. It is never a runtime image.
RUN cd /build/source/wing && \
    CGO_ENABLED=0 go test -c -mod=readonly -trimpath -tags isolated_acceptance \
      -o /build/dae-preparation.test ./dae && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath \
      -tags 'isolated_acceptance,embedallowed' \
      -ldflags '-s -w -X github.com/daeuniverse/dae-wing/db.AppName=daed -X github.com/daeuniverse/dae-wing/db.AppVersion=source-build' \
      -o /build/daed-isolated-test .
