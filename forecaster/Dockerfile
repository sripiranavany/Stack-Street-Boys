FROM python:3.12-slim
WORKDIR /app
ENV PYTHONUNBUFFERED=1 LOKY_MAX_CPU_COUNT=2
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ebbmodel.py events.py app.py seed.py ./
COPY context ./context
USER 1000
EXPOSE 8000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000"]
