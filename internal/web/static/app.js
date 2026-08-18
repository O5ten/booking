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

	// --- Live slot list while typing an own length ---------------------------
	// The page works without this: the form submits normally and the server
	// renders the same result. This only saves the trip.
	var lengthForm = document.querySelector('form[data-live-duration]');
	var slotArea = document.getElementById('slot-area');
	var feedback = document.getElementById('custom-feedback');

	if (lengthForm && slotArea && feedback && window.fetch && window.AbortController) {
		var input = lengthForm.querySelector('input[name="langd"]');
		var submit = lengthForm.querySelector('button[type="submit"]');
		var pending = null;
		var timer = null;
		var latest = 0;

		// Pressing Enter still submits, so the button is redundant now.
		if (submit) { submit.hidden = true; }

		var apply = function (html, url) {
			var doc = new DOMParser().parseFromString(html, 'text/html');
			var nextFeedback = doc.getElementById('custom-feedback');
			var nextSlots = doc.getElementById('slot-area');
			if (!nextFeedback || !nextSlots) { return; }

			feedback.innerHTML = nextFeedback.innerHTML;

			// A rejected length leaves the times alone rather than snapping
			// them back to a default, which would jump around mid-typing.
			if (!nextFeedback.querySelector('.alert')) {
				slotArea.innerHTML = nextSlots.innerHTML;
				history.replaceState(null, '', url);
			}
		};

		var update = function () {
			if (!input.value.trim()) { return; }

			var url = lengthForm.getAttribute('action') + '?' +
				new URLSearchParams(new FormData(lengthForm)).toString();

			if (pending) { pending.abort(); }
			pending = new AbortController();

			var mine = ++latest;
			slotArea.setAttribute('aria-busy', 'true');
			slotArea.classList.add('is-loading');

			fetch(url, { signal: pending.signal, headers: { 'Accept': 'text/html' } })
				.then(function (response) {
					if (!response.ok) { throw new Error('status ' + response.status); }
					return response.text();
				})
				.then(function (html) {
					// A slower earlier request must not overwrite a newer one.
					if (mine !== latest) { return; }
					apply(html, url);
				})
				.catch(function (err) {
					if (err.name === 'AbortError') { return; }
					// Give the button back so the member is never stuck.
					if (submit) { submit.hidden = false; }
				})
				.finally(function () {
					if (mine !== latest) { return; }
					slotArea.removeAttribute('aria-busy');
					slotArea.classList.remove('is-loading');
				});
		};

		input.addEventListener('input', function () {
			clearTimeout(timer);
			timer = setTimeout(update, 350);
		});
		// Enter should act immediately rather than wait out the debounce.
		lengthForm.addEventListener('submit', function (event) {
			event.preventDefault();
			clearTimeout(timer);
			update();
		});
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
