export interface ReaderLocation { page: string; query: string; fragment: string }

/** Per viewer effect/channel: retain the host's opaque initial tuple for repeated
 * normalized initial echoes only until the first different tuple. All later
 * reports stay exact, including a return to that browser spelling. A reload
 * creates a fresh tracker. No percent decoding is involved. */
export class ReaderLocationTracker {
  private initialEcho = true;
  private readonly browser: URL;
  private readonly initial: ReaderLocation;
  constructor(initial: ReaderLocation) {
    this.initial = initial;
    this.browser = new URL("/" + (initial.query ? "?" + initial.query : "") + (initial.fragment ? "#" + initial.fragment : ""), "http://artifact.invalid");
  }
  observe(observed: ReaderLocation): ReaderLocation {
    if (observed.page !== this.initial.page || observed.query !== this.browser.search.slice(1) || observed.fragment !== this.browser.hash.slice(1)) this.initialEcho = false;
    return this.initialEcho ? { ...this.initial } : observed;
  }
}
