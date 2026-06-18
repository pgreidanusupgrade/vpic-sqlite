.PHONY: build-db convert build run clean

# Step 1: build the postgres image with NHTSA data loaded
build-db:
	docker compose build db

# Step 2: start postgres, run the converter, stop postgres
# Produces api/vpic.sqlite
convert: build-db
	docker compose up -d db
	docker compose run --rm converter
	docker compose down

# Step 3: build the final API image (embeds api/vpic.sqlite)
build:
	docker build -t vpic-api ./api

# Step 4: run the API
run:
	docker run --rm -p 8080:8080 vpic-api

# All in one
all: convert build run

clean:
	docker compose down -v
	rm -f api/vpic.sqlite
