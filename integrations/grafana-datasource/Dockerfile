# Grafana 13 image with the MDDB datasource plugin preinstalled.
# Build context expects the plugin already bundled at ./dist (run `make build` first).
FROM grafana/grafana:13.1.2

USER root

ENV GF_PLUGIN_ID=tradik-mddb-datasource
ENV GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=${GF_PLUGIN_ID}

COPY dist /var/lib/grafana/plugins/${GF_PLUGIN_ID}

RUN chown -R grafana:root /var/lib/grafana/plugins/${GF_PLUGIN_ID}

USER grafana

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://localhost:3000/api/health || exit 1
