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

Usage of ws4redis:
  -heartbeats=false: Use heartbeats
  -port=9050: Listen port
  -redis-addr="localhost:6379": Redis address
  -redis-db=0: Redis db
  -redis-network="tcp": Redis network
  -redis-prefix="ws": Redis prefix
  -scale=false: Use all cpus
  -strict=false: Allow only white-listed facilities
  -timeout=15s: Heartbeat timeout
```
#### Statistics
To see daemon stats just curl /stat 
```
$ curl localhost:9050/stat
ws4redis
Version 1.1-production
Facilities 2
	facility launcher:
		 19605 clients
	facility launcher-staff:
		 0 clients
Clients 19605
CPU 24
Goroutines 78431
Memory
	Alloc 274784024
	TotalAlloc 61953223872
	Heap 274784024
	HeapSys 319307776
```

### Performance
20k clients on 1 core = 600mb

4 goroutines and 30kb per client
