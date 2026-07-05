FROM python:3.14-slim

LABEL io.airbyte.name=airbyte/destination-mddb \
      io.airbyte.version=0.1.1 \
      org.opencontainers.image.title="Airbyte Destination MDDB" \
      org.opencontainers.image.source="https://github.com/tradik/airbyte" \
      org.opencontainers.image.licenses="BSD-3-Clause"

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /airbyte/integration_code

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*

COPY requirements.txt ./
RUN pip install -r requirements.txt

COPY main.py ./
COPY destination_mddb ./destination_mddb

RUN useradd --uid 1000 --create-home airbyte \
 && chown -R airbyte:airbyte /airbyte
USER airbyte

ENV AIRBYTE_ENTRYPOINT="python /airbyte/integration_code/main.py"
ENTRYPOINT ["python", "/airbyte/integration_code/main.py"]
