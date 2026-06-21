# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -o /out/production-orders ./examples/production-orders

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/production-orders /production-orders
USER nonroot:nonroot
ENTRYPOINT ["/production-orders"]
