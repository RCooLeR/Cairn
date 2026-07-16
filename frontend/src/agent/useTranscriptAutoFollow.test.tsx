import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";

import { useTranscriptAutoFollow } from "./useTranscriptAutoFollow";

function TranscriptHarness() {
  const [revision, setRevision] = useState(0);
  const [hasContent, setHasContent] = useState(true);
  const { handleScroll, hasUnseenContent, jumpToLatest, transcriptRef } =
    useTranscriptAutoFollow(revision, hasContent);
  return (
    <>
      <div
        data-testid="transcript"
        onScroll={handleScroll}
        ref={transcriptRef}
      />
      {hasUnseenContent ? (
        <button onClick={jumpToLatest}>Jump to latest</button>
      ) : null}
      <button onClick={() => setRevision((current) => current + 1)}>
        Add content
      </button>
      <button onClick={() => setHasContent(false)}>Clear content</button>
    </>
  );
}

function setTranscriptSize(
  transcript: HTMLElement,
  scrollHeight: number,
  clientHeight = 200,
) {
  Object.defineProperty(transcript, "scrollHeight", {
    configurable: true,
    value: scrollHeight,
  });
  Object.defineProperty(transcript, "clientHeight", {
    configurable: true,
    value: clientHeight,
  });
}

describe("useTranscriptAutoFollow", () => {
  it("follows new content only while the reader remains near the bottom", () => {
    render(<TranscriptHarness />);
    const transcript = screen.getByTestId("transcript");

    setTranscriptSize(transcript, 1000);
    transcript.scrollTop = 790;
    fireEvent.scroll(transcript);
    setTranscriptSize(transcript, 1200);
    fireEvent.click(screen.getByRole("button", { name: "Add content" }));
    expect(transcript.scrollTop).toBe(1200);
    expect(
      screen.queryByRole("button", { name: "Jump to latest" }),
    ).not.toBeInTheDocument();

    transcript.scrollTop = 300;
    fireEvent.scroll(transcript);
    setTranscriptSize(transcript, 1400);
    fireEvent.click(screen.getByRole("button", { name: "Add content" }));
    expect(transcript.scrollTop).toBe(300);
    expect(
      screen.getByRole("button", { name: "Jump to latest" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Jump to latest" }));
    expect(transcript.scrollTop).toBe(1400);
    expect(
      screen.queryByRole("button", { name: "Jump to latest" }),
    ).not.toBeInTheDocument();
  });

  it("resets follow state when the conversation is cleared", () => {
    render(<TranscriptHarness />);
    const transcript = screen.getByTestId("transcript");
    setTranscriptSize(transcript, 1000);
    transcript.scrollTop = 100;
    fireEvent.scroll(transcript);
    fireEvent.click(screen.getByRole("button", { name: "Add content" }));
    expect(
      screen.getByRole("button", { name: "Jump to latest" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Clear content" }));
    expect(transcript.scrollTop).toBe(0);
    expect(
      screen.queryByRole("button", { name: "Jump to latest" }),
    ).not.toBeInTheDocument();
  });
});
