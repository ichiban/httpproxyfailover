# httpproxyfailover

A proxy server for HTTP proxies that picks out one of available HTTP proxies.

## Installation

### As a command

```console
go get github.com/ichiban/httpproxyfailover/cmd/httpproxyfailover
```

### As a library

```console
go get github.com/ichiban/httpproxyfailover
```

## Usage

### Fail over

Let's say we have 2 HTTP proxies running, namely `http://localhost:8081` and `http://localhost:8082`.
We use the first one mainly but in case that the first one is down, we want to use the second one.

```console
$ curl -D- -p -x http://localhost:8081 https://httpbin.org/status/200
HTTP/1.1 502 Bad Gateway
Content-Type: text/plain; charset=utf-8
X-Content-Type-Options: nosniff
Date: Sun, 13 Sep 2020 05:46:00 GMT
Content-Length: 1

curl: (56) Received HTTP code 502 from proxy after CONNECT
```

```console
$ curl -D- -p -x http://localhost:8082 https://httpbin.org/status/200
HTTP/1.1 200 OK
Content-Length: 0
Date: Sun, 13 Sep 2020 05:46:36 GMT

HTTP/2 200 
date: Sun, 13 Sep 2020 05:46:36 GMT
content-type: text/html; charset=utf-8
content-length: 0
server: gunicorn/19.9.0
access-control-allow-origin: *
access-control-allow-credentials: true

```

By using `httpproxyfailover`, the trial and error shown above can be done automatically.

```console
$ httpproxyfailover -p 8080 http://localhost:8081 http://localhost:8082
```

```console
$ curl -D- -p -x http://localhost:8080 https://httpbin.org/status/200
HTTP/1.1 200 OK
Date: Sun, 13 Sep 2020 06:01:39 GMT
Content-Length: 0

HTTP/2 200 
date: Sun, 13 Sep 2020 06:01:40 GMT
content-type: text/html; charset=utf-8
content-length: 0
server: gunicorn/19.9.0
access-control-allow-origin: *
access-control-allow-credentials: true

```

### Tags

By prepending curly-bracketed words in front of the URLs, you can assign tags to the backend proxies.

```console
$ httpproxyfailover -p 8080 '{foo}http://localhost:8081' '{bar}http://localhost:8082' '{foo}{bar}http://localhost:8083'
```

You can specify a tag in username part of the request.
`curl -xp http://foo@localhost:8080 https://httpbin.org/status/200` will try `http://localhost:8081` and
`http://localhost:8083` while `curl -px http://bar@localhost:8080 https://httpbin.org/status/200` tries
`http://localhost:8082` and `http://localhost:8083`.

You can also specify multiple tags.
`curl -xp http://foo,bar@localhost:8080 https://httpbin.org/status/200` will try `http://localhost:8083` only.

### Variables

Similar to tags, you can also use variables in the backend URLs.

```console
$ httpproxyfailover -p 8080 'http://{foo}.{domain}:8081' 'http://{bar}.{domain}:8082'
```

`curl -px http://domain=example.com,foo=proxy1@localhost:8080 https://httpbin.org/status/200` will try `http://proxy1.example.com:8081`
while `curl -px http://domain=example.com,bar=proxy2@localhost:8080 https://httpbin.org/status/200` tries `http://proxy2.example.com:8082`

## License

Distributed under the MIT license. See ``LICENSE`` for more information.

## Contributing

1. Fork it (<https://github.com/ichiban/httpproxyfailover/fork>)
2. Create your feature branch (`git checkout -b feature/fooBar`)
3. Commit your changes (`git commit -am 'Add some fooBar'`)
4. Push to the branch (`git push origin feature/fooBar`)
5. Create a new Pull Request