# GPS Data Ingestor

Simple application that ingests GPS raw data sent to the server and saves it as timestamped text files.

## Technology Stack

- Python/FastAPI
- Nginx (reverse proxy)
- Docker & Docker Compose

## Quick Start

1. Clone the repository
2. Run with Docker Compose:

   ```bash
   docker-compose up --build
   ```

3. The application will be available at `http://localhost`

## Data Storage

GPS data is stored in the `./data` directory as timestamped text files.

## Health Check

Check application status at: `http://localhost/health`
