FROM golang:1.23.1-bullseye AS withnode

RUN apt-get update && \ 
apt-get install -y ca-certificates net-tools && \
curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs

RUN npm init -y && npm install && npx playwright@latest install-deps

ENV GO111MODULE=on
#ENV CGO_ENABLED=1
ENV GOOS=linux
ENV GOARCH=amd64

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN go build -o /usr/bin/google-maps-scraper

RUN PLAYWRIGHT_INSTALL_ONLY=1 google-maps-scraper

ENTRYPOINT ["google-maps-scraper"]
