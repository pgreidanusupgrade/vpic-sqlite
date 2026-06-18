.PHONY: build-db convert build run clean

# Step 1: build the postgres image — downloads latest NHTSA VPIC lite at build time
build-db:
	podman build -t vpic-db ./db

# Step 2: start postgres, run the converter, stop postgres
# Produces api/vpic.sqlite
convert: build-db
	podman run -d --name vpic-db-tmp -e POSTGRES_DB=vpic -e POSTGRES_USER=vpic -e POSTGRES_PASSWORD=vpic -p 5432:5432 vpic-db
	until podman exec vpic-db-tmp pg_isready -U vpic -d vpic; do sleep 1; done
	podman build -t vpic-converter ./converter
	podman run --rm --network host -e DATABASE_URL="postgres://vpic:vpic@localhost:5432/vpic?sslmode=disable" -e OUTPUT_PATH=/out/vpic.sqlite -v "$(PWD)/api:/out" vpic-converter
	podman stop vpic-db-tmp
	podman rm vpic-db-tmp

# Step 3: build the final API image (embeds api/vpic.sqlite)
build:
	podman compose build

# Step 4: run the API on :8080
run:
	podman compose up

# All in one
all: convert build run

clean:
	podman compose down
	rm -f api/vpic.sqlite
