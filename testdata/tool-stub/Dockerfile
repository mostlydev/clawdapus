FROM python:3.12-slim

WORKDIR /app

COPY server.py ./server.py
COPY .claw-describe.json /.claw-describe.json

EXPOSE 8080

CMD ["python", "/app/server.py"]
