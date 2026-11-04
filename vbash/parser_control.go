package vbash

func (p *parser) parseFor() (*Compound, error) {
	open, err := p.next()
	if err != nil {
		return nil, err
	}
	c := &Compound{Kind: CompoundFor, Pos: open.Pos}
	tok, err := p.peek(0)
	if err != nil {
		return nil, err
	}
	name, ok := plainText(tok)
	if !ok || !validAssignName(name) || name == "" {
		return nil, p.unexpected(tok)
	}
	if _, err := p.next(); err != nil {
		return nil, err
	}
	c.Name = name
	if err := p.skipNewlines(); err != nil {
		return nil, err
	}
	tok, err = p.peek(0)
	if err != nil {
		return nil, err
	}
	if text, isWord := plainText(tok); isWord && text == "in" {
		if _, err := p.next(); err != nil {
			return nil, err
		}
		for {
			tok, err = p.peek(0)
			if err != nil {
				return nil, err
			}
			if p.endsCommand(tok) {
				break
			}
			if tok.Kind != TokWord {
				return nil, p.unexpected(tok)
			}
			if _, err := p.next(); err != nil {
				return nil, err
			}
			c.Words = append(c.Words, Word{Segments: tok.Segments(), Text: tok.Text, Pos: tok.Pos, End: tok.End})
		}
	}
	if err := p.skipSeparators(); err != nil {
		return nil, err
	}
	if _, err := p.expect("do"); err != nil {
		return nil, err
	}
	p.pushStop("done")
	body, err := p.parseList(ctxTop)
	if err != nil {
		return nil, err
	}
	c.Body = body
	if _, err := p.expect("done"); err != nil {
		return nil, err
	}
	p.popStop(1)
	return c, nil
}

func (p *parser) skipSeparators() error {
	for {
		tok, err := p.peek(0)
		if err != nil {
			return err
		}
		if tok.Kind == TokNewline {
			if _, err := p.next(); err != nil {
				return err
			}
			continue
		}
		if isOp(tok, OpSemi) {
			if _, err := p.next(); err != nil {
				return err
			}
			continue
		}
		return nil
	}
}

func (p *parser) parseWhile(until bool) (*Compound, error) {
	open, err := p.next()
	if err != nil {
		return nil, err
	}
	kind := CompoundWhile
	if until {
		kind = CompoundUntil
	}
	c := &Compound{Kind: kind, Pos: open.Pos}
	p.pushStop("do")
	cond, err := p.parseList(ctxTop)
	if err != nil {
		return nil, err
	}
	p.popStop(1)
	c.Cond = cond
	if err := p.skipSeparators(); err != nil {
		return nil, err
	}
	if _, err := p.expect("do"); err != nil {
		return nil, err
	}
	p.pushStop("done")
	body, err := p.parseList(ctxTop)
	if err != nil {
		return nil, err
	}
	c.Body = body
	if _, err := p.expect("done"); err != nil {
		return nil, err
	}
	p.popStop(1)
	return c, nil
}

func (p *parser) parseIf() (*Compound, error) {
	open, err := p.next()
	if err != nil {
		return nil, err
	}
	c := &Compound{Kind: CompoundIf, Pos: open.Pos}
	p.pushStop("then")
	cond, err := p.parseList(ctxTop)
	if err != nil {
		return nil, err
	}
	p.popStop(1)
	c.Cond = cond
	if err := p.skipSeparators(); err != nil {
		return nil, err
	}
	if _, err := p.expect("then"); err != nil {
		return nil, err
	}
	p.pushStop("elif", "else", "fi")
	body, err := p.parseList(ctxTop)
	if err != nil {
		return nil, err
	}
	c.Body = body
	for {
		tok, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		text, ok := plainText(tok)
		if !ok {
			return nil, p.unexpected(tok)
		}
		switch text {
		case "elif":
			if _, err := p.next(); err != nil {
				return nil, err
			}
			p.pushStop("then")
			econd, err := p.parseList(ctxTop)
			if err != nil {
				return nil, err
			}
			p.popStop(1)
			if err := p.skipSeparators(); err != nil {
				return nil, err
			}
			if _, err := p.expect("then"); err != nil {
				return nil, err
			}
			ebody, err := p.parseList(ctxTop)
			if err != nil {
				return nil, err
			}
			c.Elifs = append(c.Elifs, &CondClause{Cond: econd, Body: ebody, Pos: tok.Pos})
			continue
		case "else":
			if _, err := p.next(); err != nil {
				return nil, err
			}
			els, err := p.parseList(ctxTop)
			if err != nil {
				return nil, err
			}
			c.Else = els
			if _, err := p.expect("fi"); err != nil {
				return nil, err
			}
			p.popStop(1)
			return c, nil
		case "fi":
			if _, err := p.next(); err != nil {
				return nil, err
			}
			p.popStop(1)
			return c, nil
		}
		return nil, p.unexpected(tok)
	}
}

func (p *parser) expect(word string) (Token, error) {
	tok, err := p.peek(0)
	if err != nil {
		return Token{}, err
	}
	text, ok := plainText(tok)
	if !ok || text != word {
		if tok.Kind == TokEOF {
			return Token{}, &SyntaxError{Msg: "缺少 '" + word + "'", Token: word, Pos: p.pos()}
		}
		return Token{}, p.unexpected(tok)
	}
	return p.next()
}
