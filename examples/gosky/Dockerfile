FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gosky ./cmd/gosky

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=builder /out/gosky /app/gosky
COPY etc /app/etc
ENV TZ={{.Timezone}}
EXPOSE 8080 8081
USER nonroot:nonroot
ENTRYPOINT ["/app/gosky"]
