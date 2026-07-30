# Build stage
FROM node:24.18.0-alpine3.23 AS builder

WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

# Production stage
FROM node:24.18.0-alpine3.23

WORKDIR /app
RUN npm install --global --omit=dev serve@14.2.6 \
    && npm cache clean --force
COPY --from=builder --chown=node:node /app/dist ./dist
COPY --chown=node:node serve.json ./dist/serve.json

EXPOSE 3000
ENV NODE_ENV=production
USER node
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=6 \
  CMD wget -q -O /dev/null http://127.0.0.1:3000/ || exit 1
CMD ["serve", "-c", "serve.json", "dist", "-l", "3000"]
