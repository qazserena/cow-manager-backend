FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gms ./cmd/gms

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/gms /app/gms
COPY config /app/config
ENV GMS_CONFIG=/app/config/prod.json
EXPOSE 8080
ENTRYPOINT ["/app/gms"]
