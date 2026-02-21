FROM golang:1.23-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
COPY vendor/ ./vendor/

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o /hookbase-operator ./cmd/operator/

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /hookbase-operator /hookbase-operator
USER 65532:65532
ENTRYPOINT ["/hookbase-operator"]
