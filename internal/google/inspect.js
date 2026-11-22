(() => {
	const clickable = 'button, a[href], [role=button], [role=link], [role=checkbox], [role=radio], [role=option], [role=menuitem], [data-challengetype]';
	const clean = s => (s || '').replace(/\s+/g, ' ').trim();
	const visible = el => {
		const r = el.getBoundingClientRect();
		return r.width > 1 && r.height > 1 && r.right > 0 && r.bottom > 0 && el.checkVisibility({checkOpacity: true, checkVisibilityCSS: true});
	};
	const usable = el => visible(el) && !el.disabled && el.getAttribute('aria-disabled') !== 'true' && !el.closest('footer, [aria-hidden=true]');

	for (const el of document.querySelectorAll('[data-signin-ref]')) el.removeAttribute('data-signin-ref');
	let refs = 0;
	const ref = el => {
		el.setAttribute('data-signin-ref', ++refs);
		return `[data-signin-ref="${refs}"]`;
	};

	const fields = [...document.querySelectorAll('input, textarea')]
		.filter(el => usable(el) && !el.readOnly && !/^(hidden|submit|button|reset|image|file|checkbox|radio|range|color)$/.test(el.type))
		.map(el => ({
			selector: ref(el),
			label: clean(el.getAttribute('aria-label') || el.labels?.[0]?.innerText || el.placeholder || el.name),
			secret: el.type === 'password',
		}));

	const options = [...document.querySelectorAll(clickable)]
		.filter(el => usable(el) && el.target !== '_blank' && !el.parentElement.closest(clickable))
		.map(el => ({selector: ref(el), label: clean(el.innerText || el.getAttribute('aria-label'))}))
		.filter(o => o.label);

	const known = new Set([...fields, ...options].map(x => x.label));
	const lines = [];
	let block;
	const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
	while (walker.nextNode()) {
		const node = walker.currentNode;
		const el = node.parentElement;
		if (!node.data.trim() || el.closest(`${clickable}, footer, script, style, noscript, select, template`) || !visible(el)) continue;
		let b = el;
		while (b.parentElement && getComputedStyle(b).display.startsWith('inline')) b = b.parentElement;
		if (b === block) lines[lines.length - 1] += node.data;
		else lines.push(node.data);
		block = b;
	}

	return {
		url: location.href,
		busy: !!document.querySelector('[aria-busy=true]') || [...document.querySelectorAll('[role=progressbar]')].some(visible),
		lines: [...new Set(lines.map(clean))].filter(l => l && !known.has(l)),
		fields,
		options,
	};
})()
