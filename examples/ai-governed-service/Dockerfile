# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -o /out/ai-governed-service ./examples/ai-governed-service

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/ai-governed-service /ai-governed-service
USER nonroot:nonroot
ENTRYPOINT ["/ai-governed-service"]
