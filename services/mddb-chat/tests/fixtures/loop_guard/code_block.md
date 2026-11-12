Here is a Compose file with three identical workers. Each one reads the same queue, so they scale out without any coordination:

```yaml
services:
  worker-1:
    image: registry.example.com/team/worker:2.19.1
    restart: unless-stopped
    environment:
      QUEUE_URL: amqp://guest:guest@rabbitmq:5672/
      LOG_LEVEL: info
    logging:
      driver: json-file
      options:
        max-size: 10m
  worker-2:
    image: registry.example.com/team/worker:2.19.1
    restart: unless-stopped
    environment:
      QUEUE_URL: amqp://guest:guest@rabbitmq:5672/
      LOG_LEVEL: info
    logging:
      driver: json-file
      options:
        max-size: 10m
  worker-3:
    image: registry.example.com/team/worker:2.19.1
    restart: unless-stopped
    environment:
      QUEUE_URL: amqp://guest:guest@rabbitmq:5672/
      LOG_LEVEL: info
    logging:
      driver: json-file
      options:
        max-size: 10m
```

And the same three lines of shell, run once per worker, check that each one is healthy:

~~~bash
docker compose exec worker-1 /usr/local/bin/worker --health-check --timeout 5s
docker compose exec worker-2 /usr/local/bin/worker --health-check --timeout 5s
docker compose exec worker-3 /usr/local/bin/worker --health-check --timeout 5s
docker compose exec worker-1 /usr/local/bin/worker --health-check --timeout 5s
docker compose exec worker-2 /usr/local/bin/worker --health-check --timeout 5s
docker compose exec worker-3 /usr/local/bin/worker --health-check --timeout 5s
~~~

If one of them reports `unhealthy`, restart only that one with `docker compose restart worker-2`; the queue keeps the messages it had not acknowledged, so nothing is lost.
