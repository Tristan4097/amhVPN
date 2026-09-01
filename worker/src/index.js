const commonHeaders = {
  "Access-Control-Allow-Origin": "*",
  "Cache-Control": "no-store",
  "X-Content-Type-Options": "nosniff",
};

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (request.method === "OPTIONS") {
      return new Response(null, {
        status: 204,
        headers: {
          ...commonHeaders,
          "Access-Control-Allow-Methods": "GET, HEAD, OPTIONS",
          "Access-Control-Allow-Headers": "*",
        },
      });
    }

    if (url.pathname === "/health") {
      return new Response("ok", {
        status: 200,
        headers: {
          ...commonHeaders,
          "Content-Type": "text/plain; charset=utf-8",
        },
      });
    }

    if (url.pathname === "/sub") {
      const subscription = (env.SUBSCRIPTION_DATA || "").trim();

      if (!subscription) {
        return new Response("amhVPN subscription is not configured yet.", {
          status: 503,
          headers: {
            ...commonHeaders,
            "Content-Type": "text/plain; charset=utf-8",
          },
        });
      }

      return new Response(subscription + "\n", {
        status: 200,
        headers: {
          ...commonHeaders,
          "Content-Type": "text/plain; charset=utf-8",
          "Content-Disposition": 'inline; filename="amhvpn-sub.txt"',
        },
      });
    }

    return new Response(
      JSON.stringify(
        {
          service: "amhVPN Subscription",
          status: "online",
          subscription: "/sub",
          health: "/health",
        },
        null,
        2,
      ),
      {
        status: 200,
        headers: {
          ...commonHeaders,
          "Content-Type": "application/json; charset=utf-8",
        },
      },
    );
  },
};
