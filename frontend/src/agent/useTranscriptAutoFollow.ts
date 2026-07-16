import { useCallback, useLayoutEffect, useRef } from "react";

const transcriptBottomThreshold = 56;

export function useTranscriptAutoFollow(
  contentRevision: unknown,
  hasContent: boolean,
) {
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const unseenIndicatorRef = useRef<HTMLDivElement | null>(null);
  const shouldFollowRef = useRef(true);

  const setUnseenIndicatorVisible = useCallback((visible: boolean) => {
    const indicator = unseenIndicatorRef.current;
    if (indicator) {
      indicator.hidden = !visible;
    }
  }, []);

  const jumpToLatest = useCallback(() => {
    const transcript = transcriptRef.current;
    if (transcript) {
      transcript.scrollTop = transcript.scrollHeight;
    }
    shouldFollowRef.current = true;
    setUnseenIndicatorVisible(false);
  }, [setUnseenIndicatorVisible]);

  const handleScroll = useCallback(() => {
    const transcript = transcriptRef.current;
    if (!transcript) {
      return;
    }
    const distanceFromBottom =
      transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight;
    const isNearBottom = distanceFromBottom <= transcriptBottomThreshold;
    shouldFollowRef.current = isNearBottom;
    if (isNearBottom) {
      setUnseenIndicatorVisible(false);
    }
  }, [setUnseenIndicatorVisible]);

  useLayoutEffect(() => {
    const transcript = transcriptRef.current;
    if (!transcript) {
      return;
    }
    if (!hasContent) {
      transcript.scrollTop = 0;
      shouldFollowRef.current = true;
      setUnseenIndicatorVisible(false);
      return;
    }
    if (shouldFollowRef.current) {
      transcript.scrollTop = transcript.scrollHeight;
      setUnseenIndicatorVisible(false);
      return;
    }
    setUnseenIndicatorVisible(true);
  }, [contentRevision, hasContent, setUnseenIndicatorVisible]);

  return {
    handleScroll,
    jumpToLatest,
    transcriptRef,
    unseenIndicatorRef,
  };
}
