Websocket for redis
=======
[![Build Status](https://travis-ci.org/ernado/ws4redis.svg)](https://travis-ci.org/ernado/ws4redis)
[![Coverage Status](https://coveralls.io/repos/ernado/ws4redis/badge.svg?branch=master)](https://coveralls.io/r/ernado/ws4redis?branch=master)


Replacement for [django-ws-redis](https://github.com/jrief/django-websocket-redis) daemon.
See [implementation](#implementation) if you want to re-implement daemon functionality or it subset

### Installation
[Build](#building) or [download](https://github.com/destiny-development/ws4redis/releases/latest) binary.

Just copy executable to target machine.

### Building
You need [installed go compiler](http://golang.org/doc/install) and valid [$GOPATH](https://golang.org/doc/code.html#GOPATH) environment variable
```bash
export $GOPATH=~/go # or any another folder with write access
# just skip it you have already exported GOPATH variable
go get -u github.com/destiny-development/ws4redis
# `go get` will download sources and all deps, then build the binary
./$GOPATH/bin/ws4redis -h
# or add $GOPATH/bin to $PATH and just
ws4redis -h
# sources are in $GOPATH/src/github.com/destiny-development/ws4redis
# to update, just
go get -u github.com/destiny-development/ws4redis
```

### Usage

```bash
$ ws4redis -h

Usage of ./ws4redis:
  -default="launcher": Default facility
  -facilities="launcher,launcher-staff": Permitted facilities for strict mode
  -heartbeats=false: Use heartbeats
  -max-size=32: Maximum message size
  -port=9050: Listen port
  -redis-addr="localhost:6379": Redis addr
  -redis-db=0: Redis db
  -redis-network="tcp": Redis network
  -redis-prefix="ws": Redis prefix
  -scale=false: Use all cpus
  -strict=false: Allow only white-listed facilities
  -timeout=10s: Heartbeat timeout
```

### Statistics

To see daemon stats just curl /stat
```
$ curl localhost:9050/stat
ws4redis
Version 1.2-production
Facilities 2
	facility launcher:
		 22505 clients
	facility launcher-staff:
		 1 clients
Clients 22506
CPU 24
Goroutines 45022
Memory
	Alloc 195528640
	TotalAlloc 3720837240
	Heap 195528640
	HeapSys 299450368
```

### Performance
22k clients on 1 core = 453mb

2 goroutines and 30kb per client


### Implementation
Server listens for websocket connections on `{host}:{port}/{prefix}/{facility}`,
e.g. `ws.host.com/ws/launcher`,
expects heartbeats every 5s (by default, timeout is 10s) and acts as echo-server.

#### Timeout
If no heartbeats are sent by client in {timeout}, server closes websocket connection

#### Broadcasting
For every facility server creates redis connection and starts listening on
channel `{redis-prefix}:broadcast:{facility}` (e.g. `ws:broadcast:launcher`).
Every message published on this channel is sent to all clients of {facility} without any changes.
