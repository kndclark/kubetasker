# Use an official Python runtime as a parent image.
# The 'slim' variant is smaller and good for production.
FROM python:3.12-slim

# Set environment variables
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

# Set the working directory in the container
WORKDIR /app

# Copy only the requirements file first to leverage Docker's build cache.
# This layer is only rebuilt if the requirements file changes.
COPY requirements.txt .

# Install the Python dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Copy the rest of the application code into the container
COPY . /app/

# Expose the port that uvicorn will run on
EXPOSE 8000

# Command to run the application using uvicorn.
# We bind to 0.0.0.0 to make it accessible from outside the container.
CMD ["uvicorn", "listener:app", "--host", "0.0.0.0", "--port", "8000"]
