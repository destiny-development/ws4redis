Websocket for redis
=======
Replacement for [django-ws-redis](https://github.com/jrief/django-websocket-redis) daemon.

### Building
You need [installed go compiler](http://golang.org/doc/install)
```bash
go get github.com/ernado/ws4redis
ws4redis -h
# or
git clone https://github.com/ernado/ws4redis.git
cd ws4redis
go build
./ws4redis -h
```

### Installation
Just copy binary file to target machine via scp or something else.

#### Usage

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
#### Statistics
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
