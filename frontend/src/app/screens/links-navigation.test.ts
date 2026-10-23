import { test } from "node:test";
import assert from "node:assert/strict";
import { ReaderLinksNavigation } from "./links-navigation.ts";
import type { ReaderPosition } from "./reader-navigation.ts";

function setup() {
  const flow = new ReaderLinksNavigation();
  flow.bind("document/version/page/region/Links");
  let release!: (position: ReaderPosition) => void;
  const deferred = new Promise<ReaderPosition>(done => { release = done; });
  let active = true, route = "/documents/d/versions/v?page=a#region=r", authority = true;
  const history: ReaderPosition[] = [];
  return { flow, release, history, deactivate: () => { active = false; }, move: () => { route += "changed"; }, deny: () => { authority = false; },
    request: () => flow.request({ active: () => active, route: () => route,
      guard: () => { if (!authority) throw Error("stale session"); },
      readPosition: () => deferred, navigate: position => { history.push(position); },
    }),
  };
}

test("BL F5 navigation discards deferred personal reads after context or activation changes", async () => {
    for (const changed of ["version", "page", "region", "tab", "closed", "route", "activation", "changed-back"]) {
      const f = setup(), pending = f.request();
      if (changed === "closed") f.flow.bind(null);
      else if (changed === "route") f.move();
      else if (changed === "activation") f.deactivate();
      else {
        f.flow.bind(changed);
        if (changed === "changed-back") f.flow.bind("document/version/page/region/Links");
      }
      f.release({ scroll_x: 7, scroll_y: 91 }); await pending;
      assert.deepEqual(f.history, [], `${changed} must not push history or a stale return position`);
    }
});

test("BL F5 final authority check and unchanged-context return position", async () => {
  const denied = setup(), stale = denied.request(); denied.deny();
  denied.release({ scroll_x: 1, scroll_y: 2 }); await assert.rejects(stale, /stale session/);
  assert.deepEqual(denied.history, []);
  const current = setup(), pending = current.request();
  current.release({ scroll_x: 7, scroll_y: 91 }); await pending;
  assert.deepEqual(current.history, [{ scroll_x: 7, scroll_y: 91 }]);
});
