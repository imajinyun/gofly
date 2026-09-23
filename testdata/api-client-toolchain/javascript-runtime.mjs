import assert from 'node:assert/strict';
import http from 'node:http';
import { pathToFileURL } from 'node:url';

const clientModule = process.argv[2];
if (!clientModule) throw new Error('generated client path is required');
const { APIClient } = await import(pathToFileURL(clientModule));
const requests = [];

const server = http.createServer(async (request, response) => {
  let body = '';
  for await (const chunk of request) body += chunk;
  requests.push({ method: request.method, url: request.url, headers: request.headers, body });
  if (request.url.startsWith('/api/v1/items/fail')) {
    response.writeHead(422, { 'Content-Type': 'text/plain' });
    response.end('rejected');
    return;
  }
  if (request.method === 'POST') {
    response.writeHead(201, { 'Content-Type': 'application/json' });
    response.end('{"id":"created"}');
    return;
  }
  if (request.method === 'DELETE') {
    response.writeHead(204);
    response.end();
    return;
  }
  response.writeHead(200, { 'Content-Type': 'application/json' });
  response.end('{"id":"healthy"}');
});

await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
try {
  const address = server.address();
  const client = new APIClient(`http://127.0.0.1:${address.port}`);
  const item = { sku: 'sku-1', labels: { color: ['blue'] } };

  assert.deepEqual(await client.createItem({
    requestID: 'req-create', tenantID: 'tenant-a', id: 'sku /1', dryRun: true, item,
  }), { id: 'created' });
  assert.deepEqual(await client.health(), { id: 'healthy' });
  assert.equal(await client.deleteItem({
    requestID: 'req-delete', tenantID: 'tenant-b', id: 'delete/1', dryRun: false, item,
  }), undefined);
  await assert.rejects(
    client.createItem({ id: 'fail', item }),
    (error) => error instanceof Error && error.message === 'rejected',
  );

  assert.equal(requests[0].method, 'POST');
  assert.equal(requests[0].url, '/api/v1/items/sku%20%2F1?dryRun=true');
  assert.equal(requests[0].headers['x-request-id'], 'req-create');
  assert.equal(requests[0].headers['x-tenant-id'], 'tenant-a');
  assert.deepEqual(JSON.parse(requests[0].body), { item });
  assert.equal(requests[1].method, 'GET');
  assert.equal(requests[1].url, '/api/v1/health');
  assert.equal(requests[2].method, 'DELETE');
  assert.equal(requests[2].url, '/api/v1/items/delete%2F1?dryRun=false');
  assert.equal(requests[2].headers['x-request-id'], 'req-delete');
  assert.equal(requests[2].headers['x-tenant-id'], 'tenant-b');
  assert.deepEqual(JSON.parse(requests[2].body), { item });
  assert.equal(requests[3].method, 'POST');
  assert.equal(requests[3].url, '/api/v1/items/fail');
  console.log('javascript runtime OK');
} finally {
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
}
