FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -buildvcs=true \
    -ldflags "-s -w" \
    -o /out/ai-token-exporter \
    ./cmd/ai-token-exporter

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/ai-token-exporter /usr/local/bin/ai-token-exporter

EXPOSE 9108

ENTRYPOINT ["/usr/local/bin/ai-token-exporter"]
