const UPSTREAM_SUBSCRIPTION =
  "https://raw.githubusercontent.com/morpheusadam/v2ray-config/main/subs/bundles/mini.txt";

const commonHeaders = {
  "Access-Control-Allow-Origin": "*",
  "Cache-Control": "no-store",
  "X-Content-Type-Options": "nosniff",
};

function textResponse(body, status = 200, extraHeaders = {}) {
  return new Response(body, {
    status,
    headers: {
      ...commonHeaders,
      "Content-Type": "text/plain; charset=utf-8",
      ...extraHeaders,
    },
  });
}

export default {
  async fetch(request) {
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
      return textResponse("ok");
    }

    if (url.pathname === "/sub") {
      try {
        const upstream = await fetch(UPSTREAM_SUBSCRIPTION, {
          headers: {
            "User-Agent": "amhVPN-Subscription-Worker/1.0",
            "Accept": "text/plain,*/*",
          },
          cf: {
            cacheTtl: 300,
            cacheEverything: true,
          },
        });

        if (!upstream.ok) {
          return textResponse(
            `upstream subscription returned HTTP ${upstream.status}`,
            502,
          );
        }

        const body = (await upstream.text()).trim();

        if (!body) {
          return textResponse("upstream subscription is empty", 502);
        }

        return textResponse(body + "\n", 200, {
          "Content-Disposition": 'inline; filename="amhvpn-sub.txt"',
          "X-Subscription-Source": "public-github",
        });
      } catch (error) {
        return textResponse(
          `could not fetch upstream subscription: ${error?.message || "unknown error"}`,
          502,
        );
      }
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
