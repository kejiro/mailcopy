FROM golang:1.12-alpine AS build
RUN apk add --no-cache curl git gcc musl-dev
WORKDIR /app
COPY . .
RUN go build -o mailcopy

FROM alpine
RUN apk add --no-cache ca-certificates
COPY --from=build /app/mailcopy /usr/local/bin/
RUN mkdir -p /etc/mailcopy
ENV CONFIG_FILE=/etc/mailcopy/config.json
CMD ["/usr/local/bin/mailcopy"]
