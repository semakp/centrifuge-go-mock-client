FROM golang:1.10

WORKDIR $GOPATH/src/github.com/semakp/centrifuge-go-mock-client

COPY . .

RUN go get -d -v ./...

RUN go install -v ./...

EXPOSE 8080

CMD ["centrifuge-go-mock-client"]