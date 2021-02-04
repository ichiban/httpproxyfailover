# httpproxyfailover

Create a fault-tolerant HTTP proxy out of multiple somewhat unreliable HTTP proxies.

## Installation

```console
go get github.com/ichiban/httpproxyfailover/cmd/httpproxyfailover
```

## Usage

```console
$ httpproxyfailover --help
Usage: httpproxyfailover [options...] <backend proxy URI template>...
  -p, --port int           Specify port number to listen on (random if not specified)
  -t, --timeout duration   Set timeout for each trial
  -T, --tls                Check TLS handshake
pflag: help requested
```

## Features

### Fail over

Let's say we have 2 HTTP proxies running, namely `http://localhost:8081` and `http://localhost:8082`.
The first one is currently unavailable and returns 502 on CONNECT.

```console
$ curl -w "%{http_code}\n" -px http://localhost:8081 https://httpbin.org/status/200
000
curl: (56) Received HTTP code 502 from proxy after CONNECT
```

While the second one works fine.

```console
$ curl -w "%{http_code}\n" -px http://localhost:8082 https://httpbin.org/status/200
200
```

By using `httpproxyfailover`, the trial and error shown above can be done automatically.

```console
$ httpproxyfailover -p 8080 http://localhost:8081 http://localhost:8082
INFO[0000] start                                         addr="[::]:8080" timeout=0s tlsHandshake=false
WARN[0005] fail-over                                     error="502 Bad Gateway" from="[::1]:62453" to="httpbin.org:443" via="http://localhost:8081"
INFO[0005] connect                                       from="[::1]:62453" to="httpbin.org:443" via="http://localhost:8082"
```

```console
$ curl -w "%{http_code}\n" -px http://localhost:8080 https://httpbin.org/status/200
200
```

### TLS handshake

If you're working with untrustworthy proxies, they might try MITM attacks. In that case, HTTPS requests over the proxy
connection always fail because of unsuccessful TLS handshakes.

With `--tls`(`-T`) option, `httpproxyfailover` will skip those proxies with unsuccessful TLS handshakes.

### favicon

Even though you have established a proxy connection, it may not be able to actually access the contents of the origin
server through it.

With `--favicon`(`-f`) option, `httpproxyfailover` will skip those proxies which can't GET `/favicon.ico`.

### Tags

By prepending curly-bracketed words in front of the URLs, you can assign tags to the backend proxies.

```console
$ httpproxyfailover -p 8080 '{foo}http://localhost:8081' '{bar}http://localhost:8082' '{foo}{bar}http://localhost:8083' 'http://localhost:8084'
```

You can specify a tag in username part of the `httpproxyfailover` URL.

`httpproxyfailover` tries a backend proxy if the tags in the template are all provided in the username part.

So, if you provide `foo`, `httpproxyfailover` will try proxies without tags other than `foo`.

```console
$ curl -w "%{http_code}\n" -px http://foo@localhost:8080 https://httpbin.org/status/200
200
```

```console
$ httpproxyfailover -p 8080 '{foo}http://localhost:8081' '{bar}http://localhost:8082' '{foo}{bar}http://localhost:8083' 'http://localhost:8084'
INFO[0000] start                                         addr="[::]:8080" timeout=0s tlsHandshake=false
WARN[0007] fail-over                                     error="502 Bad Gateway" from="[::1]:62578" to="httpbin.org:443" via="http://localhost:8081"
INFO[0008] connect                                       from="[::1]:62578" to="httpbin.org:443" via="http://localhost:8084"
```

Or if you provide `bar`, then it'll try proxies without tags other than `bar`.

```console
$ curl -w "%{http_code}\n" -px http://bar@localhost:8080 https://httpbin.org/status/200
200
```

```console
$ httpproxyfailover -p 8080 '{foo}http://localhost:8081' '{bar}http://localhost:8082' '{foo}{bar}http://localhost:8083' 'http://localhost:8084'
INFO[0000] start                                         addr="[::]:8080" timeout=0s tlsHandshake=false
WARN[0002] fail-over                                     error="502 Bad Gateway" from="[::1]:62601" to="httpbin.org:443" via="http://localhost:8082"
INFO[0003] connect                                       from="[::1]:62601" to="httpbin.org:443" via="http://localhost:8084"
```

You can also specify multiple tags. If you provide `foo,bar`, then it'll try proxies without tags other than `foo` or `bar`.

```console
$ curl -w "%{http_code}\n" -px http://foo,bar@localhost:8080 https://httpbin.org/status/200
200
```

```console
$ httpproxyfailover -p 8080 '{foo}http://localhost:8081' '{bar}http://localhost:8082' '{foo}{bar}http://localhost:8083' 'http://localhost:8084'
INFO[0000] start                                         addr="[::]:8080" timeout=0s tlsHandshake=false
WARN[0010] fail-over                                     error="502 Bad Gateway" from="[::1]:62610" to="httpbin.org:443" via="http://localhost:8081"
WARN[0010] fail-over                                     error="502 Bad Gateway" from="[::1]:62610" to="httpbin.org:443" via="http://localhost:8082"
WARN[0010] fail-over                                     error="502 Bad Gateway" from="[::1]:62610" to="httpbin.org:443" via="http://localhost:8083"
INFO[0010] connect                                       from="[::1]:62610" to="httpbin.org:443" via="http://localhost:8084"
```

### Variables

Similar to tags, you can also use variables in the backend URLs.
Actually tags are shorthand for variables with empty string values.

```console
$ httpproxyfailover -p 8080 'http://{domain}:8081' 'http://{domain}:8082'
```

```console
$ curl -w "%{http_code}\n" -px http://domain=localhost@localhost:8080 https://httpbin.org/status/200
200
```

```console
$ httpproxyfailover -p 8080 'http://{domain}:8081' 'http://{domain}:8082'
INFO[0000] start                                         addr="[::]:8080" timeout=0s tlsHandshake=false
WARN[0012] fail-over                                     error="502 Bad Gateway" from="[::1]:62672" to="httpbin.org:443" via="http://localhost:8081"
INFO[0012] connect                                       from="[::1]:62672" to="httpbin.org:443" via="http://localhost:8082"
```

## License

Distributed under the MIT license. See ``LICENSE`` for more information.

## Contributing

1. Fork it (<https://github.com/ichiban/httpproxyfailover/fork>)
2. Create your feature branch (`git checkout -b feature/fooBar`)
3. Commit your changes (`git commit -am 'Add some fooBar'`)
4. Push to the branch (`git push origin feature/fooBar`)
5. Create a new Pull Request