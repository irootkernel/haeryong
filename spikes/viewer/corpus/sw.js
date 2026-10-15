// Authored root-scope worker; caches are version-local because origins stay bound.
self.addEventListener('install',e=>e.waitUntil(self.skipWaiting()));
self.addEventListener('activate',e=>e.waitUntil(self.clients.claim()));
self.addEventListener('fetch',e=>{if(new URL(e.request.url).pathname==='/authored-value'){e.respondWith(new Response('authored same-version runtime response'));}});
