FROM python:3.12-slim

WORKDIR /app

COPY forecast/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY series.py forecast/infer.py ./

CMD ["python", "-u", "infer.py"]
