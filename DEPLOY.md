# Quick Deployment Guide

## Prerequisites

- Go 1.21+ installed
- Node.js 18+ and npm installed
- Git installed
- Speechmatics API Key

## Quick Start

### 1. Clone the repository
```bash
git clone https://github.com/soaringjerry/DreamTrans.git
cd DreamTrans
```

### 2. Setup Backend (Go)
```bash
cd backend

# Create .env file
echo "SM_API_KEY=your_api_key_here" > .env
echo "PORT=8080" >> .env  # Change to your desired port

# Install dependencies
go mod download

# Run the backend
go run main.go
```

### 3. Setup Frontend (React)
Open a new terminal:
```bash
cd frontend

# Install dependencies
npm install

# Create .env file (important if using custom backend port)
cp .env.example .env
# Edit .env to set backend URL if not using default ports:
# VITE_BACKEND_URL=http://localhost:YOUR_BACKEND_PORT
# VITE_BACKEND_WS_URL=ws://localhost:YOUR_BACKEND_PORT

# For production build:
npm run build
npm run preview -- --host 0.0.0.0 --port 3000  # Change port as needed

# OR for development:
npm run dev -- --host 0.0.0.0 --port 5173  # Change port as needed
```

### 4. Access the Application
- Frontend: http://your-server-ip:3000 (production) or http://your-server-ip:5173 (dev)
- Backend API: http://your-server-ip:8080

## Production Deployment with PM2

### Backend
```bash
# Install PM2 globally
npm install -g pm2

# Build Go binary
cd backend
go build -o dreamtrans-backend

# Start with PM2
pm2 start ./dreamtrans-backend --name dreamtrans-backend
```

### Frontend
```bash
cd frontend
npm run build

# Serve with PM2
pm2 serve dist 3000 --spa --name dreamtrans-frontend
```

### Save PM2 configuration
```bash
pm2 save
pm2 startup
```

## Docker Deployment (Alternative)

Create a `docker-compose.yml`:
```yaml
version: '3.8'

services:
  backend:
    build: ./backend
    ports:
      - "8080:8080"
    environment:
      - SM_API_KEY=${SM_API_KEY}
    restart: unless-stopped

  frontend:
    build: ./frontend
    ports:
      - "3000:3000"
    depends_on:
      - backend
    restart: unless-stopped
```

Then run:
```bash
# Set your API key
export SM_API_KEY=your_api_key_here

# Start services
docker-compose up -d
```

## Nginx Reverse Proxy (Recommended)

```nginx
server {
    listen 80;
    server_name your-domain.com;

    # Frontend
    location / {
        proxy_pass http://localhost:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
    }

    # Backend API
    location /api {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
    }

    # WebSocket for translation
    location /ws {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

## Mobile/Remote Access Setup

To access the application from mobile devices or other computers on the same network:

### Frontend Configuration
```bash
# Edit frontend/.env
VITE_BACKEND_URL=http://YOUR_COMPUTER_IP:8080
VITE_BACKEND_WS_URL=ws://YOUR_COMPUTER_IP:8080

# Start frontend with host binding
npm run dev -- --host 0.0.0.0 --port 5173
```

### Finding Your Computer's IP
```bash
# On Linux/Mac
ip addr show | grep inet
# or
ifconfig | grep inet

# On Windows
ipconfig
```

### Access from Mobile
1. Ensure your phone is on the same WiFi network
2. Open browser and navigate to: `http://YOUR_COMPUTER_IP:5173`
3. Allow microphone permissions when prompted

## Security Notes

1. **Never expose the Speechmatics API key in frontend code**
2. Use HTTPS in production (Let's Encrypt recommended)
3. Configure CORS properly in production (backend already includes CORS support)
4. Set up firewall rules to only expose necessary ports
5. For production, update CORS settings in `backend/main.go` to only allow specific origins instead of "*"

## Troubleshooting

### Backend won't start
- Check if port 8080 is available: `lsof -i :8080`
- Verify .env file exists with valid API key
- Check Go version: `go version`

### Frontend connection issues
- Ensure backend is running first
- Check if frontend can reach backend: `curl http://localhost:8080/api/token/rt`
- Verify WebSocket connections are not blocked by firewall

### Audio not working
- Browser must have microphone permissions
- HTTPS is required for microphone access in production
- Check browser console for errors

## Quick Health Check
```bash
# Check backend
curl http://localhost:8080/api/token/rt

# Should return a JWT token if working correctly
```