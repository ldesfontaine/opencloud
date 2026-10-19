import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { followPublicLive, pollEvery, reopenEvery, type LiveStream } from "./publicLive";

const connecting = 0;
const closed = 2;

class FakeStream implements LiveStream {
  readyState = connecting;
  closed = false;
  private listeners = new Map<string, (() => void)[]>();

  addEventListener(type: string, listener: () => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  close() {
    this.closed = true;
    this.readyState = closed;
  }

  emit(type: string) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener();
    }
  }

  // Le serveur refuse le flux (429, 503) : le navigateur le ferme pour de bon.
  refuse() {
    this.readyState = closed;
    this.emit("error");
  }
}

function follow() {
  const streams: FakeStream[] = [];
  const reload = vi.fn();
  const stop = followPublicLive(() => {
    const fake = new FakeStream();
    streams.push(fake);
    return fake;
  }, reload);
  // Le flux ouvert en n-ième, en partant de zéro.
  const stream = (index: number) => {
    const opened = streams[index];
    if (opened === undefined) {
      throw new Error(`stream ${index} never opened`);
    }
    return opened;
  };
  return { streams, stream, reload, stop };
}

describe("followPublicLive", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("rereads on each status event while the stream lives, and polls nothing", () => {
    const { streams, stream, reload } = follow();
    stream(0).emit("connected");
    stream(0).emit("status");
    vi.advanceTimersByTime(10 * pollEvery);
    expect(reload).toHaveBeenCalledTimes(1);
    expect(streams).toHaveLength(1);
  });

  it("polls every minute once refused, and tries the stream again after five", () => {
    const { streams, stream, reload } = follow();
    stream(0).refuse();
    vi.advanceTimersByTime(pollEvery);
    expect(reload).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(reopenEvery - pollEvery - 1);
    expect(streams).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(streams).toHaveLength(2);
    expect(stream(0).closed).toBe(true);
    expect(reload).toHaveBeenCalledTimes(5);

    // Le direct revient : la relecture périodique s'arrête après une dernière.
    stream(1).emit("connected");
    expect(reload).toHaveBeenCalledTimes(6);
    vi.advanceTimersByTime(10 * pollEvery);
    expect(reload).toHaveBeenCalledTimes(6);
  });

  it("does not stack polls or retries when the stream is refused again", () => {
    const { streams, stream, reload } = follow();
    stream(0).refuse();
    vi.advanceTimersByTime(reopenEvery);
    stream(1).refuse();
    reload.mockClear();
    vi.advanceTimersByTime(pollEvery);
    expect(reload).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(reopenEvery);
    expect(streams).toHaveLength(3);
  });

  it("lets the browser reconnect by itself when a stream reaches its end", () => {
    const { streams, stream, reload } = follow();
    stream(0).emit("connected");
    stream(0).emit("error");
    stream(0).emit("reconnected");
    expect(reload).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(reopenEvery);
    expect(streams).toHaveLength(1);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("leaves no timer and no stream behind once stopped", () => {
    const { streams, stream, reload, stop } = follow();
    stream(0).refuse();
    stop();
    vi.advanceTimersByTime(10 * reopenEvery);
    expect(reload).not.toHaveBeenCalled();
    expect(streams).toHaveLength(1);
    expect(stream(0).closed).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });
});
