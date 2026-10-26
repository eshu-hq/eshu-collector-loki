# Standalone Jira collector image: a static pure-Go binary, built reproducibly
# with -trimpath, on a base with no shell and no package manager. The binary is
# its own installer: `collector seed --dest DIR` writes DIR/collector (this
# binary, mode 0755) and DIR/manifest.yaml, so an init container can seed the
# component home without anything else in the image.
#
#   docker run IMAGE                      serves one claim from stdin
#   docker run IMAGE seed --dest /home    seeds the binary and manifest
#   ... seed --dest /home --image-ref REPO@sha256:<digest>
#       (or COLLECTOR_IMAGE_REF) also stamps the deployed digest into the
#       seeded manifest; the image cannot embed its own digest.
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/collector ./cmd/collector

# Base digests resolved 2026-09-17; re-resolve them when bumping either base.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/collector /usr/local/bin/collector
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/collector"]
CMD ["--sdk-stdio"]
