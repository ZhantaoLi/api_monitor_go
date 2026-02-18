FROM golang:1.24-alpine AS builder
WORKDIR /src

# Copy source and resolve dependencies
COPY go.mod ./
COPY *.go ./
COPY web/ web/

RUN go mod tidy && go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/api-monitor .

# ---- Runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/api-monitor /usr/local/bin/api-monitor

WORKDIR /app
EXPOSE 8081
CMD ["api-monitor"]
