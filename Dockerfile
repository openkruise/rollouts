# Build the manager binary
FROM golang:1.19-alpine3.17 AS builder

ADD . /workspace/
WORKDIR /workspace

# Build
RUN CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build -installsuffix cgo -a -o manager main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details

FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /workspace/manager /manager
COPY lua_configuration /lua_configuration
USER 65532:65532

ENTRYPOINT ["/manager"]
