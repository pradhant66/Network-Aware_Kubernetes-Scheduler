FROM python:3.9-slim

WORKDIR /app

# Copy requirements and install them
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy the application code
COPY app.py .

# Expose the port the Flask app runs on
EXPOSE 8080

# Command to run the application
CMD ["python", "app.py"]