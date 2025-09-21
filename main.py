import time
import os
from typing import Any

import uvicorn
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel


app = FastAPI(
    title="GPS Data Ingestor",
    description="Simple API for ingesting data and saving to timestamped files"
)

class DataPayload(BaseModel):
    data: Any

@app.post("/")
async def ingest_data(data: Any):
    try:
        timestamp = int(time.time())
        filename = f"data_{timestamp}.txt"

        os.makedirs("data", exist_ok=True)

        with open(f"data/{filename}", "w") as f:
            f.write(str(data))

        return {
            "message": "Data ingested successfully",
            "filename": filename,
            "timestamp": timestamp
        }
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error saving data: {str(e)}")

@app.get("/health")
async def health():
    return {"message": "GPS Data Ingestor web app works"}

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8000)
