// Grow with content, including pasted text and programmatic clears. Observe
// width changes so wrapping remains correct when a dialog or viewport resizes.
export function autosize(node: HTMLTextAreaElement, _value: string) {
	let frame = 0;
	let previousWidth = -1;
	function resize() {
		if (!node.getClientRects().length) return;
		const minimum = 96;
		const maximum = Math.max(minimum, Math.min(280, window.innerHeight * 0.35));
		node.style.height = '0px';
		node.style.height = `${Math.min(maximum, Math.max(minimum, node.scrollHeight))}px`;
		node.style.overflowY = node.scrollHeight > node.clientHeight ? 'auto' : 'hidden';
	}
	function schedule() {
		cancelAnimationFrame(frame);
		frame = requestAnimationFrame(resize);
	}
	const observer = new ResizeObserver(([entry]) => {
		if (entry.contentRect.width !== previousWidth) {
			previousWidth = entry.contentRect.width;
			schedule();
		}
	});
	observer.observe(node);
	node.addEventListener('input', resize);
	window.addEventListener('resize', schedule);
	schedule();
	return {
		update: schedule,
		destroy() {
			cancelAnimationFrame(frame);
			observer.disconnect();
			node.removeEventListener('input', resize);
			window.removeEventListener('resize', schedule);
		}
	};
}
