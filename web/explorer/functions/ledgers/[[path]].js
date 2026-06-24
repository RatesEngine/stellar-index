// CF Pages Function for /ledgers/* — serves real assets first (the /ledgers/
// list, or future pre-rendered recent ledgers), else the shell. See
// functions/transactions/[[path]].js for the full rationale (SEO plan D1;
// Spike A proved a _redirects catch-all clobbers static files).
export async function onRequest(context) {
  const { request, env } = context;
  const url = new URL(request.url);

  const asset = await env.ASSETS.fetch(request);
  if (asset.status !== 404) {
    return asset;
  }

  const shell = await env.ASSETS.fetch(
    new Request(new URL('/ledgers/shell/', url.origin), request),
  );
  return new Response(shell.body, { status: 200, headers: shell.headers });
}
