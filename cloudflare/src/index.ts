// Runs the Go MCP server as a Cloudflare Container behind a Worker.
//
// Every request goes to one named container instance, because MCP sessions and
// the Trading 212 response cache live in the server's memory. Authentication
// stays in the Go server: the Worker forwards requests, including the
// Authorization header, unchanged.

import { Container, getContainer } from "@cloudflare/containers";

interface Env {
  TRADING212: DurableObjectNamespace<Trading212>;
  // Plain vars from wrangler.jsonc.
  ENVIRONMENT: string;
  LOG_LEVEL: string;
  // Secrets, set with `wrangler secret put`.
  TRADING212_API_KEY: string;
  TRADING212_API_SECRET?: string;
  MCP_AUTH_TOKENS: string;
}

export class Trading212 extends Container<Env> {
  defaultPort = 8080;
  // Idle time before the container stops. Stopping drops MCP sessions and the
  // cache; clients reinitialise on their next call.
  sleepAfter = "1h";

  envVars = {
    TRANSPORT: "streamable-http",
    TRADING212_HTTP_ADDR: "0.0.0.0:8080",
    LOG_FORMAT: "json",
    ENVIRONMENT: this.env.ENVIRONMENT,
    LOG_LEVEL: this.env.LOG_LEVEL,
    TRADING212_API_KEY: this.env.TRADING212_API_KEY,
    TRADING212_API_SECRET: this.env.TRADING212_API_SECRET ?? "",
    MCP_AUTH_TOKENS: this.env.MCP_AUTH_TOKENS,
  };
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    return getContainer(env.TRADING212).fetch(request);
  },
} satisfies ExportedHandler<Env>;
