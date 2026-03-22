# Build stage
FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

# Production stage
FROM nginx:alpine
# Copy the built React app to Nginx's web root
COPY --from=builder /app/dist /usr/share/nginx/html
# We'll use a template file so we can inject the backend URL at runtime via Cloud Run
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
