FROM postgres:16-alpine AS db-init

ENV POSTGRES_DB=vpic
ENV POSTGRES_USER=vpic
ENV POSTGRES_PASSWORD=vpic
ENV PGDATA=/var/lib/postgresql/data

RUN apk add --no-cache curl unzip

# Download the current month's NHTSA VPIC release.
# URL pattern: https://vpic.nhtsa.dot.gov/downloads/vPICList_{full|lite}_{YYYY}_{MM}.plain.zip
# Tries full first; falls back to lite if full is not published for this month.
# Override at build time: podman build --build-arg VPIC_URL=https://... ./db
ARG VPIC_URL=
RUN set -e; \
    if [ -n "$VPIC_URL" ]; then \
        echo "Using provided VPIC_URL: $VPIC_URL"; \
        curl -fsSL "$VPIC_URL" -o /tmp/vpic.zip; \
    else \
        YEAR=$(date +%Y); MONTH=$(date +%m); \
        FULL="https://vpic.nhtsa.dot.gov/downloads/vPICList_full_${YEAR}_${MONTH}.plain.zip"; \
        LITE="https://vpic.nhtsa.dot.gov/downloads/vPICList_lite_${YEAR}_${MONTH}.plain.zip"; \
        echo "Trying full: $FULL"; \
        curl -fsSL "$FULL" -o /tmp/vpic.zip \
            || (echo "Full not available, falling back to lite: $LITE" && curl -fsSL "$LITE" -o /tmp/vpic.zip); \
    fi; \
    unzip -o /tmp/vpic.zip -d /tmp/init/ \
    && rm /tmp/vpic.zip

USER postgres
RUN initdb -D "$PGDATA" \
    && echo "host all all 0.0.0.0/0 md5" >> "$PGDATA/pg_hba.conf" \
    && pg_ctl start -D "$PGDATA" -o "-c listen_addresses='*'" -w -t 120 \
    && psql -U postgres -c "CREATE ROLE vpic WITH LOGIN PASSWORD 'vpic';" \
    && psql -U postgres -c "CREATE DATABASE vpic OWNER vpic;" \
    && for f in $(ls /tmp/init/*.sql 2>/dev/null | sort); do \
         echo "Loading $f ..."; \
         psql -U vpic -d vpic -f "$f"; \
       done \
    && pg_ctl stop -D "$PGDATA" -w

FROM postgres:16-alpine

ENV POSTGRES_DB=vpic
ENV POSTGRES_USER=vpic
ENV POSTGRES_PASSWORD=vpic
ENV PGDATA=/var/lib/postgresql/data

COPY --chown=postgres:postgres --from=db-init /var/lib/postgresql/data /var/lib/postgresql/data
