FROM postgres:16-alpine AS db-init

ENV POSTGRES_DB=vpic
ENV POSTGRES_USER=vpic
ENV POSTGRES_PASSWORD=vpic
ENV PGDATA=/var/lib/postgresql/data

RUN apk add --no-cache curl unzip

# Download the latest NHTSA VPIC lite release directly from the source.
# This URL always returns the current monthly release as a zip.
RUN curl -fsSL "https://vpic.nhtsa.dot.gov/api/users/cacheupdate/export?type=full" \
        -o /tmp/vpic.zip \
    && unzip -o /tmp/vpic.zip -d /tmp/init/ \
    && rm /tmp/vpic.zip

USER postgres
RUN initdb -D "$PGDATA" \
    && pg_ctl start -D "$PGDATA" -o "-c listen_addresses=''" -w -t 120 \
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
