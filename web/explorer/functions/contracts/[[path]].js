// CF Pages Function for /contracts/* — serves real assets first (the /contracts/
// directory, or future pre-rendered active contracts), else the shell. See
// functions/transactions/[[path]].js for the full rationale (SEO plan D1).
export async function onRequest(context) {
  const { request, env } = context;
  const url = new URL(request.url);

  const asset = await env.ASSETS.fetch(request);
  if (asset.status !== 404) {
    return asset;
  }

  const shell = await env.ASSETS.fetch(
    new Request(new URL('/contracts/shell/', url.origin), request),
  );
  return new Response(shell.body, { status: 200, headers: shell.headers });
}
