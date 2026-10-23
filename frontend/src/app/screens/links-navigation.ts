import type { ReaderPosition } from "./reader-navigation.ts";

/** A click stays owned by its panel activation and exact host context through
 * the final personal-position read. Returning to an old context cannot revive it. */
export class ReaderLinksNavigation {
  #context: string | null = null;
  #generation = 0;
  bind(context: string | null): void {
    if (context !== this.#context) this.#generation++;
    this.#context = context;
  }
  async request(options: {
    active(): boolean; guard(): void; route(): string;
    readPosition(): Promise<ReaderPosition>;
    navigate(position: ReaderPosition): void;
  }): Promise<void> {
    if (this.#context === null) return;
    const own = this.#generation, route = options.route();
    const current = () => this.#context !== null && own === this.#generation && options.active() && options.route() === route;
    options.guard();
    if (!current()) return;
    const position = await options.readPosition();
    if (!current()) return;
    options.guard();
    options.navigate(position);
  }
}
