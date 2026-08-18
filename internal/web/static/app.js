// Small progressive enhancements. The site works without any of this.
(function () {
	'use strict';

	// --- Theme toggle -------------------------------------------------------
	var root = document.documentElement;
	var stored = null;
	try { stored = localStorage.getItem('rb-theme'); } catch (e) { /* private mode */ }
	if (stored === 'light' || stored === 'dark') {
		root.setAttribute('data-theme', stored);
	}
	var toggle = document.querySelector('[data-theme-toggle]');
	if (toggle) {
		toggle.addEventListener('click', function () {
			var dark = root.getAttribute('data-theme') === 'dark' ||
				(root.getAttribute('data-theme') !== 'light' &&
					window.matchMedia('(prefers-color-scheme: dark)').matches);
			var next = dark ? 'light' : 'dark';
			root.setAttribute('data-theme', next);
			try { localStorage.setItem('rb-theme', next); } catch (e) { /* ignore */ }
		});
	}

	// --- Confirm before destructive submits ---------------------------------
	document.querySelectorAll('form[data-confirm]').forEach(function (form) {
		form.addEventListener('submit', function (event) {
			if (!window.confirm(form.getAttribute('data-confirm'))) {
				event.preventDefault();
			}
		});
	});

	// --- Keep the chosen day in view ----------------------------------------
	var selected = document.querySelector('.daytab.is-selected');
	if (selected && selected.scrollIntoView) {
		selected.scrollIntoView({ block: 'nearest', inline: 'center' });
	}

	// --- Guest rooms: keep check-out after check-in --------------------------
	var from = document.getElementById('fran');
	var to = document.getElementById('till');
	if (from && to) {
		var sync = function () {
			if (!from.value) { return; }
			var min = new Date(from.value);
			min.setDate(min.getDate() + 1);
			to.min = min.toISOString().slice(0, 10);
			if (to.value && to.value <= from.value) { to.value = to.min; }
		};
		from.addEventListener('change', sync);
		sync();
	}
})();
