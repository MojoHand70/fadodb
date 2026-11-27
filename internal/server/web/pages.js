// The one script of the server's pages (ADR-0088, item 7). A form that
// makes or uses a passkey holds the server's question in data-question;
// the script hands it to the browser, which asks the person for their
// fingerprint, their face or the device's PIN, and sends the answer
// back in the form's field "response". It proposes the name of a device
// from the browser, and says in words what went wrong. It reads no
// cookie, and calls nothing itself but the server, on a page that shows
// the code for the phone, to ask how the code stands: the form goes to
// the server, as any form of these pages does.
(function () {
  'use strict';

  // WebAuthn's JSON writes bytes in base64url; the browser takes them as
  // bytes.
  function bytes(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) s += '=';
    var raw = atob(s), out = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out.buffer;
  }
  function text(buf) {
    var b = new Uint8Array(buf), s = '';
    for (var i = 0; i < b.length; i++) s += String.fromCharCode(b[i]);
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function toMake(q) {
    q.challenge = bytes(q.challenge);
    q.user.id = bytes(q.user.id);
    (q.excludeCredentials || []).forEach(function (c) { c.id = bytes(c.id); });
    return q;
  }
  function toUse(q) {
    q.challenge = bytes(q.challenge);
    (q.allowCredentials || []).forEach(function (c) { c.id = bytes(c.id); });
    return q;
  }
  function made(c) {
    return { id: c.id, rawId: text(c.rawId), type: c.type, response: {
      clientDataJSON: text(c.response.clientDataJSON), attestationObject: text(c.response.attestationObject) } };
  }
  function used(c) {
    return { id: c.id, rawId: text(c.rawId), type: c.type, response: {
      clientDataJSON: text(c.response.clientDataJSON), authenticatorData: text(c.response.authenticatorData),
      signature: text(c.response.signature), userHandle: c.response.userHandle ? text(c.response.userHandle) : '' } };
  }

  // What went wrong, in words a person reads.
  function words(err, make) {
    switch (err && err.name) {
      case 'NotAllowedError':
        return 'Nothing was done: the window was closed, or its time ran out. Try again.';
      case 'InvalidStateError':
        return 'This device holds a passkey of yours for this server already.';
      case 'SecurityError':
        return 'This address cannot hold a passkey. Open the server at its name, as https://' + location.hostname + ' or http://localhost, not at numbers.';
      // No authenticator the browser can reach, or none that makes a kind
      // of key the server takes.
      case 'NotSupportedError':
        return make ? 'This browser finds no device that can make a passkey here. Try your phone, or another device.'
          : 'This browser finds no passkey to use here. Try your phone, or another device.';
      case 'AbortError':
        return 'Stopped. Try again.';
    }
    return 'The passkey could not be ' + (make ? 'made' : 'used') + (err && err.message ? ': ' + err.message : '.');
  }

  // The name of a device, proposed from the browser: "Ann's iPhone".
  function device(person) {
    var ua = navigator.userAgent, kind =
      /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' :
      /Android/.test(ua) ? (/Mobile/.test(ua) ? 'Android phone' : 'Android tablet') :
      /CrOS/.test(ua) ? 'Chromebook' : /Macintosh/.test(ua) ? 'Mac' :
      /Windows/.test(ua) ? 'Windows PC' : /Linux/.test(ua) ? 'Linux computer' : 'computer';
    var first = (person || '').trim().split(/\s+/)[0];
    return first ? first + "'s " + kind : kind;
  }

  document.querySelectorAll('input[data-device]').forEach(function (input) {
    if (!input.value) input.value = device(input.getAttribute('data-device'));
  });

  // The form that signs in with a passkey of the person's the device
  // holds already takes the place of the one that makes a passkey, and
  // the words that lead to making one go.
  function openHeld(form, held) {
    var other = held && document.getElementById(held);
    if (!other) return false;
    if (form) form.hidden = true;
    other.hidden = false;
    document.querySelectorAll('[data-make-lead]').forEach(function (p) { p.hidden = true; });
    var go = other.querySelector('button[type="submit"]');
    if (go) go.focus();
    return true;
  }

  // Asked for by the person, before the browser says anything: a phone
  // may say that it holds a passkey only in a sheet of its own.
  document.querySelectorAll('[data-show-held]').forEach(function (b) {
    b.addEventListener('click', function () {
      openHeld(document.querySelector('form[data-held="' + b.getAttribute('data-show-held') + '"]'), b.getAttribute('data-show-held'));
    });
  });

  document.querySelectorAll('form[data-passkey]').forEach(function (form) {
    var said = form.querySelector('[data-said]');
    var button = form.querySelector('button[type="submit"]');
    var make = form.getAttribute('data-passkey') === 'make';
    if (!window.PublicKeyCredential || !navigator.credentials) {
      if (said) said.textContent = 'This browser makes no passkeys. Open this page in a recent Chrome, Edge, Safari or Firefox.';
      if (button) button.disabled = true;
      return;
    }
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var q = JSON.parse(form.getAttribute('data-question'));
      if (button) button.disabled = true;
      if (said) said.textContent = '';
      var asked = make ? navigator.credentials.create({ publicKey: toMake(q) }) : navigator.credentials.get({ publicKey: toUse(q) });
      asked.then(function (c) {
        form.querySelector('input[name="response"]').value = JSON.stringify(make ? made(c) : used(c));
        HTMLFormElement.prototype.submit.call(form);
      }, function (err) {
        if (button) button.disabled = false;
        // A device that holds a passkey of the person's already, which a
        // password manager may have brought there, makes no other: the
        // form that signs in with that one takes this one's place.
        var held = err && err.name === 'InvalidStateError' && form.getAttribute('data-held');
        if (openHeld(form, held)) return;
        if (said) said.textContent = words(err, make);
      });
    });
  });

  // The code for the phone: the page asks the server every two seconds
  // how the code stands. Once the phone has added its passkey, this
  // browser enters, or the account page is shown again with it. After
  // the code's 10 minutes, or when the server says it ended, the page
  // says so and asks no more.
  document.querySelectorAll('figure[data-phone]').forEach(function (fig) {
    var said = fig.querySelector('[data-phone-said]');
    var wait = fig.getAttribute('data-phone');
    var until = Date.now() + 10 * 60 * 1000;
    function ended() {
      fig.classList.add('ended');
      if (said) said.textContent = 'This code has ended. Load the page again for a new one.';
    }
    function ask() {
      if (Date.now() > until) { ended(); return; }
      fetch('/auth/phone/state', { credentials: 'same-origin', cache: 'no-store' })
        .then(function (r) { return r.json(); })
        .then(function (a) {
          if (a.state === 'done') {
            if (said) said.textContent = 'Your phone added its passkey.';
            if (wait === 'enter') {
              var enter = document.querySelector('form[data-phone-enter]');
              if (enter) HTMLFormElement.prototype.submit.call(enter);
            } else {
              location.replace(location.pathname);
            }
          } else if (a.state === 'ended') {
            ended();
          } else {
            setTimeout(ask, 2000);
          }
        }, function () { setTimeout(ask, 5000); });
    }
    setTimeout(ask, 2000);
  });
})();
