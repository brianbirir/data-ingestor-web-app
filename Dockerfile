FROM python:3.12-slim

WORKDIR /app

RUN apt-get update && apt-get install -y \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

RUN mkdir -p /app/data
RUN mkdir -p /var/log/gunicorn
RUN chown -R www-data:www-data /var/log/gunicorn

EXPOSE 8000

CMD ["gunicorn", "-c", "configs/gunicorn.dev.conf.py","--reload", "main:app"]