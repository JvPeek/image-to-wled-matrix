FROM golang:1.24 AS builder

WORKDIR /app

COPY main.go go.mod go.sum ./ 

RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -o image-to-artnet main.go 

FROM scratch AS application

WORKDIR /opt/

COPY --from=builder /app/image-to-artnet /opt/image-to-artnet

EXPOSE 8090

ENTRYPOINT [ "/opt/image-to-artnet" ]
