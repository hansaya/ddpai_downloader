FROM golang:1.20-alpine

WORKDIR /app

COPY src/go.mod ./
COPY src/go.sum ./
RUN go mod download

COPY src/*.go ./

RUN go build -o /ddpai-downloader

EXPOSE 8080

CMD [ "/ddpai-downloader" ]